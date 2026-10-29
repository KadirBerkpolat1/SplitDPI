package presets_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/KadirBerkpolat1/SplitDPI/internal/paths"
	"github.com/KadirBerkpolat1/SplitDPI/internal/presets"
)

func file(s string) *fstest.MapFile { return &fstest.MapFile{Data: []byte(s)} }

func fixture() fstest.MapFS {
	return fstest.MapFS{
		"VERSION": file("2026-10-28\n"),
		"isp/tr/as9121-turk-telekom.toml": file(`
schema   = 1
name     = "Türk Telekom"
country  = "TR"
asn      = [9121]
source   = "SplitWire-Turkey"
reported = "2026-10"

[[strategy]]
id      = "tt-multidisorder"
tcp_tls = "--payload=tls_client_hello --lua-desync=multidisorder:pos=2:seqovl=1"

[[strategy]]
id      = "tt-oob"
tcp_tls = "--in-range=-s1 --lua-desync=oob:urp=b"
`),
		"isp/tr/bad.toml":    file("schema = 1\nname = \n"),
		"isp/tr/future.toml": file("schema = 2\nname = \"Future\"\nasn = [1]\n"),
		"countries/tr.toml": file(`
schema       = 1
default_apps = ["discord", "roblox"]

[[strategy]]
id      = "tr-dup"
tcp_tls = "--payload=tls_client_hello --lua-desync=multidisorder:pos=2:seqovl=1"

[[strategy]]
id      = "tr-fake"
tcp_tls = "--payload=tls_client_hello --lua-desync=fake:blob=0x00000000:ip_ttl=5:repeats=1"
`),
		"generic.toml": file(`
schema = 1

[[strategy]]
id      = "generic-multisplit"
tcp_tls = "--payload=tls_client_hello --lua-desync=multisplit:blob=fake_default_tls:ip_ttl=5:pos=2:nodrop:repeats=1"
`),
		"apps/discord.toml": file(`
schema = 1
id     = "discord"
name   = "Discord"
hosts  = ["discord.com", "discord.gg"]
probe  = ["https://discord.com/api/v9/gateway"]

[udp]
ports  = "50000-50100,19294-19344,3478-3481,5349,1400"
l7     = ["discord", "stun"]
desync = "--payload=stun,discord_ip_discovery --lua-desync=fake:blob=fake_default_quic:repeats=6"
`),
		"exclude/global.txt": file("# global\nsteampowered.com\nmicrosoft.com\n\n"),
		"exclude/tr.txt":     file("ziraat.com.tr\nmicrosoft.com\n"),
	}
}

func ids(cs []presets.Candidate) []string {
	var out []string
	for _, c := range cs {
		out = append(out, c.Strategy.ID+"@"+c.Source)
	}
	return out
}

func TestLoad(t *testing.T) {
	s := presets.Load(fixture())
	if len(s.Warnings) != 2 {
		t.Fatalf("warnings: %q, want 2", s.Warnings)
	}
	for _, f := range []string{"isp/tr/bad.toml", "isp/tr/future.toml"} {
		if !slices.ContainsFunc(s.Warnings, func(w string) bool { return strings.Contains(w, f) }) {
			t.Errorf("no warning names %s: %q", f, s.Warnings)
		}
	}
	if len(s.ISPs) != 1 || s.ISPs[0].Name != "Türk Telekom" || s.ISPs[0].File != "isp/tr/as9121-turk-telekom.toml" {
		t.Errorf("ISPs: %+v", s.ISPs)
	}
	if d := s.Apps["discord"]; d.Name != "Discord" || d.UDP == nil || d.UDP.Ports != "50000-50100,19294-19344,3478-3481,5349,1400" {
		t.Errorf("discord: %+v", d)
	}
	if got := s.Countries["TR"].DefaultApps; !slices.Equal(got, []string{"discord", "roblox"}) {
		t.Errorf("TR default apps: %q", got)
	}
	if s.Version != "2026-10-28" {
		t.Errorf("version %q", s.Version)
	}
}

func TestCandidatesISPCountryGeneric(t *testing.T) {
	got := ids(presets.Load(fixture()).Candidates(9121, "TR"))
	want := []string{
		"tt-multidisorder@isp/tr/as9121-turk-telekom.toml",
		"tt-oob@isp/tr/as9121-turk-telekom.toml",
		"tr-fake@countries/tr.toml",
		"generic-multisplit@generic.toml",
	}
	if !slices.Equal(got, want) {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

func TestCandidatesUnknownGenericOnly(t *testing.T) {
	got := ids(presets.Load(fixture()).Candidates(1, "XX"))
	if want := []string{"generic-multisplit@generic.toml"}; !slices.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestCurrent(t *testing.T) {
	p := paths.Under(t.TempDir())
	if s := presets.Current(p); s.Origin != "embedded" || len(s.Apps) == 0 {
		t.Errorf("no cache: origin %q, %d apps", s.Origin, len(s.Apps))
	}
	if err := os.MkdirAll(filepath.Join(p.PresetCache, "apps"), 0o755); err != nil {
		t.Fatal(err)
	}
	app := "schema = 1\nid = \"cached\"\nname = \"C\"\nhosts = [\"c.com\"]\n"
	if err := os.WriteFile(filepath.Join(p.PresetCache, "apps", "cached.toml"), []byte(app), 0o644); err != nil {
		t.Fatal(err)
	}
	if s := presets.Current(p); s.Origin != "cache" || len(s.Apps) != 1 {
		t.Errorf("cache: origin %q, apps %v", s.Origin, s.Apps)
	}
}

func TestExcludeFor(t *testing.T) {
	got := presets.Load(fixture()).ExcludeFor("TR")
	want := []string{"microsoft.com", "steampowered.com", "ziraat.com.tr"}
	if !slices.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}
