package presets_test

import (
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/KadirBerkpolat1/SplitDPI/internal/presets"
)

func TestEmbeddedSeedClean(t *testing.T) {
	s := presets.Load(presets.Embedded())
	if len(s.Warnings) != 0 {
		t.Fatalf("warnings: %v", s.Warnings)
	}
	for _, id := range []string{"discord", "youtube", "roblox", "telegram"} {
		a, ok := s.Apps[id]
		if !ok {
			t.Fatalf("missing app %q", id)
		}
		if len(a.Probe) < 1 {
			t.Fatalf("app %q: want ≥1 probe", id)
		}
	}
	if s.Apps["discord"].UDP == nil {
		t.Fatal("discord: want UDP profile")
	}
	wantTLS := "--payload=tls_client_hello --lua-desync=multidisorder:pos=2:seqovl=1"
	cs := s.Candidates(9121, "TR")
	if len(cs) == 0 || cs[0].Strategy.TCPTLS != wantTLS {
		t.Fatalf("Candidates(9121,TR)[0].TCPTLS = %q, want %q", firstTLS(cs), wantTLS)
	}
	wantApps := []string{"discord", "roblox"}
	if !slices.Equal(s.Countries["TR"].DefaultApps, wantApps) {
		t.Fatalf("TR default_apps = %v, want %v", s.Countries["TR"].DefaultApps, wantApps)
	}
}

// Every embedded app can be probed, lists each host once in sorted order and
// lists no host that another of its hosts already covers.
func TestEmbeddedApps(t *testing.T) {
	s := presets.Load(presets.Embedded())
	if len(s.Warnings) != 0 {
		t.Fatalf("warnings: %v", s.Warnings)
	}
	want := []string{"discord", "facebook", "instagram", "linkedin", "pastebin", "roblox", "signal", "telegram", "tiktok", "viber", "wattpad", "whatsapp", "x", "youtube"}
	if got := slices.Sorted(maps.Keys(s.Apps)); !slices.Equal(got, want) {
		t.Fatalf("apps %v, want %v", got, want)
	}
	var quic []string
	for _, id := range want {
		a := s.Apps[id]
		if a.Name == "" || len(a.Probe) == 0 || len(a.Hosts) == 0 {
			t.Errorf("%s: name %q, %d probes, %d hosts; want all set", id, a.Name, len(a.Probe), len(a.Hosts))
		}
		if !slices.IsSorted(a.Hosts) || len(slices.Compact(slices.Clone(a.Hosts))) != len(a.Hosts) {
			t.Errorf("%s: hosts not sorted and unique: %v", id, a.Hosts)
		}
		for _, h := range a.Hosts {
			for _, p := range a.Hosts {
				if strings.HasSuffix(h, "."+p) {
					t.Errorf("%s: %s is already covered by %s", id, h, p)
				}
			}
		}
		if a.QUIC {
			quic = append(quic, id)
		}
	}
	if wantQUIC := []string{"facebook", "instagram", "linkedin", "whatsapp", "youtube"}; !slices.Equal(quic, wantQUIC) {
		t.Errorf("quic apps %v, want %v", quic, wantQUIC)
	}
}

func firstTLS(cs []presets.Candidate) string {
	if len(cs) == 0 {
		return ""
	}
	return cs[0].Strategy.TCPTLS
}
