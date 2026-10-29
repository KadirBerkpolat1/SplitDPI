package presets_test

import (
	"slices"
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

func firstTLS(cs []presets.Candidate) string {
	if len(cs) == 0 {
		return ""
	}
	return cs[0].Strategy.TCPTLS
}
