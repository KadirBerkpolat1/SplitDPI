package render_test

import (
	"errors"
	"flag"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/KadirBerkpolat1/SplitDPI/internal/config"
	"github.com/KadirBerkpolat1/SplitDPI/internal/engine"
	"github.com/KadirBerkpolat1/SplitDPI/internal/paths"
	"github.com/KadirBerkpolat1/SplitDPI/internal/presets"
	"github.com/KadirBerkpolat1/SplitDPI/internal/render"
)

var update = flag.Bool("update", false, "rewrite golden files")

const multidisorder = "--payload=tls_client_hello --lua-desync=multidisorder:pos=2:seqovl=1"

func set() presets.Set {
	return presets.Set{
		Apps: map[string]presets.App{
			"discord": {ID: "discord", Name: "Discord", Hosts: []string{"discord.com", "discord.gg"},
				UDP: &presets.UDP{
					Ports:  "50000-50100,19294-19344,3478-3481,5349,1400",
					L7:     []string{"discord", "stun"},
					Desync: "--payload=stun,discord_ip_discovery --lua-desync=fake:blob=fake_default_quic:repeats=6",
				}},
			"youtube": {ID: "youtube", Name: "YouTube", Hosts: []string{"youtube.com", "googlevideo.com"}, QUIC: true},
		},
		Exclude: map[string][]string{
			"global": {"steampowered.com", "youtube.com", "m.youtube.com", "google.com"},
			"tr":     {"ziraat.com.tr"},
		},
	}
}

func input() render.Input {
	cfg := config.Default()
	cfg.Apps = []string{"discord", "youtube"}
	cfg.Strategy = &config.Strategy{ID: "tt-multidisorder", TCPTLS: multidisorder}
	return render.Input{
		Engine:  engine.Engine{Dir: "/var/lib/splitdpi/engine/zapret2-v1.0.6", ArchDir: "linux-x86_64"},
		Cfg:     cfg,
		Set:     set(),
		Country: "TR",
		P:       paths.Under(""),
		User:    "tpws",
	}
}

func mustRender(t *testing.T, in render.Input) render.Output {
	t.Helper()
	out, err := render.Render(in)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// profiles splits the argv after the global options into --new-separated profiles.
func profiles(argv []string) [][]string {
	var ps [][]string
	cur := []string{}
	for _, a := range argv[1:] {
		switch {
		case a == "--new":
			ps = append(ps, cur)
			cur = []string{}
		case strings.HasPrefix(a, "--qnum="), strings.HasPrefix(a, "--user="), strings.HasPrefix(a, "--lua-init="):
		default:
			cur = append(cur, a)
		}
	}
	return append(ps, cur)
}

func TestRenderGolden(t *testing.T) {
	got := strings.Join(mustRender(t, input()).Argv, "\n") + "\n"
	const golden = "testdata/discord-youtube-broad.argv"
	if *update {
		if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Errorf("argv differs from %s (rerun with -update to accept):\n%s", golden, got)
	}
}

func TestProfileCount(t *testing.T) {
	newCount := func(in render.Input) int {
		n := 0
		for _, a := range mustRender(t, in).Argv {
			if a == "--new" {
				n++
			}
		}
		return n
	}
	in := input()
	if n := newCount(in); n != 2 {
		t.Errorf("broad: %d --new, want 2 (UDP, TLS, broad)", n)
	}
	in.Cfg.Mode = config.ModeApps
	if n := newCount(in); n != 1 {
		t.Errorf("apps: %d --new, want 1 (UDP, TLS)", n)
	}
	in = input()
	in.Cfg.Strategy.QUIC = "--payload=quic_initial --lua-desync=fake:blob=fake_default_quic:repeats=6"
	if n := newCount(in); n != 3 {
		t.Errorf("with QUIC: %d --new, want 3", n)
	}
}

func TestBroadLastWithAutohostlist(t *testing.T) {
	ps := profiles(mustRender(t, input()).Argv)
	last := ps[len(ps)-1]
	if !slices.Contains(last, "--hostlist-auto="+paths.Under("").AutoHostlist) {
		t.Errorf("last profile is not the broad one: %q", last)
	}
	for _, p := range ps[:len(ps)-1] {
		if slices.ContainsFunc(p, func(a string) bool { return strings.HasPrefix(a, "--hostlist-auto=") }) {
			t.Errorf("autohostlist outside the last profile: %q", p)
		}
	}
}

func TestLuaInitOnceGlobal(t *testing.T) {
	argv := mustRender(t, input()).Argv
	var inits []string
	first := slices.Index(argv, "--new")
	for i, a := range argv {
		if strings.HasPrefix(a, "--lua-init=") {
			inits = append(inits, a)
			if i > first {
				t.Errorf("--lua-init after the first --new: %q", a)
			}
		}
	}
	want := []string{
		"--lua-init=@/var/lib/splitdpi/engine/zapret2-v1.0.6/lua/zapret-lib.lua",
		"--lua-init=@/var/lib/splitdpi/engine/zapret2-v1.0.6/lua/zapret-antidpi.lua",
		"--lua-init=@/var/lib/splitdpi/engine/zapret2-v1.0.6/lua/zapret-auto.lua",
	}
	if !slices.Equal(inits, want) {
		t.Errorf("lua inits %q, want %q", inits, want)
	}
	if argv[0] != "/var/lib/splitdpi/engine/zapret2-v1.0.6/binaries/linux-x86_64/nfqws2" {
		t.Errorf("argv[0] = %q", argv[0])
	}
}

func TestExcludeEverywhere(t *testing.T) {
	in := input()
	in.Cfg.Strategy.QUIC = "--payload=quic_initial --lua-desync=fake:blob=fake_default_quic:repeats=6"
	for _, p := range profiles(mustRender(t, in).Argv) {
		if !slices.Contains(p, "--hostlist-exclude="+in.P.ExcludeFile) {
			t.Errorf("profile without --hostlist-exclude: %q", p)
		}
	}
}

func TestAppHostsLeaveExclude(t *testing.T) {
	out := mustRender(t, input())
	for _, d := range []string{"youtube.com", "m.youtube.com"} {
		if slices.Contains(out.Exclude, d) {
			t.Errorf("%s still excluded although youtube is enabled: %q", d, out.Exclude)
		}
	}
	if want := []string{"google.com", "steampowered.com", "ziraat.com.tr"}; !slices.Equal(out.Exclude, want) {
		t.Errorf("exclude %q, want %q", out.Exclude, want)
	}
	in := input()
	in.UserExclude = []string{"youtube.com"}
	if out := mustRender(t, in); !slices.Contains(out.Exclude, "youtube.com") {
		t.Errorf("user exclusion lost: %q", out.Exclude)
	}
	in.Cfg.Apps = []string{"discord"}
	if out := mustRender(t, in); !slices.Contains(out.Exclude, "m.youtube.com") {
		t.Errorf("youtube disabled but its hosts left the exclude list: %q", out.Exclude)
	}
}

// The shipped global list names whatsapp.com, x.com and youtube.com, which
// Russia's default apps need.
func TestRussiaDefaultsLeaveExclude(t *testing.T) {
	in := input()
	in.Set = presets.Load(presets.Embedded())
	in.Country = "RU"
	in.Cfg.Apps = in.Set.Countries["RU"].DefaultApps
	out := mustRender(t, in)
	for _, d := range out.Exclude {
		for _, h := range out.Hostlist {
			if d == h || strings.HasSuffix(d, "."+h) || strings.HasSuffix(h, "."+d) {
				t.Errorf("exclude %q cancels enabled host %q", d, h)
			}
		}
	}
	for _, d := range []string{"gosuslugi.ru", "vk.com", "microsoft.com"} {
		if !slices.Contains(out.Exclude, d) {
			t.Errorf("exclude lacks %q", d)
		}
	}
	for _, d := range []string{"whatsapp.com", "x.com", "youtube.com"} {
		if !slices.Contains(out.Hostlist, d) || slices.Contains(out.Exclude, d) {
			t.Errorf("%s: want in hostlist, not in exclude", d)
		}
	}
}

func TestHostlist(t *testing.T) {
	in := input()
	in.UserHosts = []string{"example.org", "discord.com"}
	out := mustRender(t, in)
	want := []string{"discord.com", "discord.gg", "example.org", "googlevideo.com", "youtube.com"}
	if !slices.Equal(out.Hostlist, want) {
		t.Errorf("hostlist %q, want %q", out.Hostlist, want)
	}
	if out.HostlistFile != in.P.HostlistFile || out.ExcludeFile != in.P.ExcludeFile {
		t.Errorf("files %q %q", out.HostlistFile, out.ExcludeFile)
	}
}

// A user site alone is enough for mode "apps": it gets the hostlist profile.
func TestHostlistUserSiteOnly(t *testing.T) {
	in := input()
	in.Cfg.Mode = config.ModeApps
	in.Cfg.Apps = nil
	in.UserHosts = []string{"pastebin.com"}
	out := mustRender(t, in)
	if !slices.Equal(out.Hostlist, []string{"pastebin.com"}) {
		t.Errorf("hostlist %q", out.Hostlist)
	}
	if !slices.Contains(out.Argv, "--hostlist="+in.P.HostlistFile) {
		t.Errorf("no hostlist profile: %q", out.Argv)
	}
}

func TestUserSiteBeatsShippedExclude(t *testing.T) {
	in := input()
	in.Set.Exclude["global"] = append(in.Set.Exclude["global"], "pastebin.com")
	in.UserHosts = []string{"pastebin.com"}
	out := mustRender(t, in)
	if slices.Contains(out.Exclude, "pastebin.com") || !slices.Contains(out.Hostlist, "pastebin.com") {
		t.Errorf("exclude %q, hostlist %q", out.Exclude, out.Hostlist)
	}
	in.UserExclude = []string{"pastebin.com"}
	if out := mustRender(t, in); !slices.Contains(out.Exclude, "pastebin.com") {
		t.Errorf("user exclude lost to user site: %q", out.Exclude)
	}
}

func TestPorts(t *testing.T) {
	out := mustRender(t, input())
	if !slices.Equal(out.TCPPorts, []string{"443"}) {
		t.Errorf("tcp %q", out.TCPPorts)
	}
	if want := []string{"50000-50100", "19294-19344", "3478-3481", "5349", "1400"}; !slices.Equal(out.UDPPorts, want) {
		t.Errorf("udp %q, want %q", out.UDPPorts, want)
	}
	in := input()
	in.Cfg.Strategy.TCPHTTP = "--payload=http_req --lua-desync=http_hostcase:spell=hoSt"
	in.Cfg.Strategy.QUIC = "--payload=quic_initial --lua-desync=fake:blob=fake_default_quic:repeats=6"
	out = mustRender(t, in)
	if !slices.Equal(out.TCPPorts, []string{"443", "80"}) {
		t.Errorf("tcp with http %q", out.TCPPorts)
	}
	if out.UDPPorts[0] != "443" {
		t.Errorf("udp with quic %q", out.UDPPorts)
	}
}

func TestErrors(t *testing.T) {
	in := input()
	in.Cfg.Strategy = nil
	if _, err := render.Render(in); !errors.Is(err, render.ErrNoStrategy) {
		t.Errorf("no strategy: %v", err)
	}

	in = input()
	in.Cfg.Mode = config.ModeApps
	in.Cfg.Apps = nil
	if _, err := render.Render(in); !errors.Is(err, render.ErrNoApps) {
		t.Errorf("no apps: %v", err)
	}

	in = input()
	in.Cfg.Apps = []string{"discord", "foo"}
	var unknown render.ErrUnknownApp
	if _, err := render.Render(in); !errors.As(err, &unknown) || unknown.ID != "foo" {
		t.Errorf("unknown app: %v", err)
	}

	for field, s := range map[string]*config.Strategy{
		"tcp_tls":  {TCPTLS: "--lua-init=@x"},
		"tcp_http": {TCPTLS: multidisorder, TCPHTTP: "--user=root"},
		"quic":     {TCPTLS: multidisorder, QUIC: "--lua-desync=luaexec:code=x"},
	} {
		in = input()
		in.Cfg.Strategy = s
		var bad render.ErrBadStrategy
		if _, err := render.Render(in); !errors.As(err, &bad) || bad.Field != field {
			t.Errorf("%s: %v", field, err)
		}
	}
}
