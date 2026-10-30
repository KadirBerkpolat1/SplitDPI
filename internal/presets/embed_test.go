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

func TestEmbeddedCountries(t *testing.T) {
	s := presets.Load(presets.Embedded())
	want := map[string][]string{
		"TR": {"discord", "roblox"},
		"RU": {"youtube", "discord", "whatsapp", "x", "viber"},
		"UZ": {"tiktok"},
		"PK": {"signal"},
		"OM": {"signal", "viber"},
		"MM": {"x", "signal"},
		"TZ": {"discord", "signal"},
		"SY": {"discord"},
	}
	if len(s.Countries) != len(want) {
		t.Errorf("%d countries, want %d", len(s.Countries), len(want))
	}
	for code, apps := range want {
		if got := s.Countries[code].DefaultApps; !slices.Equal(got, apps) {
			t.Errorf("%s default_apps = %v, want %v", code, got, apps)
		}
	}
	for code, c := range s.Countries {
		for _, id := range c.DefaultApps {
			if _, ok := s.Apps[id]; !ok {
				t.Errorf("%s: default app %q has no apps/%s.toml", code, id, id)
			}
		}
		if c.Exclude != "" && len(s.Exclude[c.Exclude]) == 0 {
			t.Errorf("%s: exclude %q is missing or empty", code, c.Exclude)
		}
	}
}

func TestEmbeddedExcludeForRU(t *testing.T) {
	s := presets.Load(presets.Embedded())
	got := s.ExcludeFor("RU")
	if !slices.IsSorted(got) || len(slices.Compact(slices.Clone(got))) != len(got) {
		t.Errorf("not sorted and unique: %q", got)
	}
	for _, d := range slices.Concat(s.Exclude["global"], s.Exclude["ru"]) {
		if !slices.Contains(got, d) {
			t.Errorf("ExcludeFor(RU) lacks %q", d)
		}
	}
	for _, d := range []string{"gosuslugi.ru", "sberbank.ru", "microsoft.com"} {
		if !slices.Contains(got, d) {
			t.Errorf("ExcludeFor(RU) lacks %q", d)
		}
	}
	if slices.Contains(got, "gov.tr") {
		t.Error("ExcludeFor(RU) includes the Turkish list")
	}
}

func firstTLS(cs []presets.Candidate) string {
	if len(cs) == 0 {
		return ""
	}
	return cs[0].Strategy.TCPTLS
}
