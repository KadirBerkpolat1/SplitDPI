package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	"github.com/KadirBerkpolat1/SplitDPI/internal/i18n"
	"github.com/KadirBerkpolat1/SplitDPI/internal/presets"
	"github.com/KadirBerkpolat1/SplitDPI/internal/probe"
)

// manyProbes is a takeover-sized list: 3 apps and 32 sites, of which 22 open,
// 5 are blocked (2 apps, 3 sites) and 8 have no address.
func manyProbes() (*fake, map[string][]probe.Result) {
	f := newFake()
	f.set = trSet()
	f.set.Apps["roblox"] = presets.App{ID: "roblox", Name: "Roblox"}
	f.cfg.Apps = []string{"discord", "roblox", "youtube"}
	reset := failedProbe()["discord"][0]
	res := map[string][]probe.Result{
		"discord":           {reset},
		"roblox":            {reset},
		"youtube":           {{URL: "https://www.youtube.com/", OK: true}},
		"a-blocked.example": {reset},
		"b-blocked.example": {reset},
		"c-blocked.example": {reset},
	}
	for i := range 21 {
		d := fmt.Sprintf("open%02d.example", i)
		res[d] = []probe.Result{{URL: probe.SiteURL(d), OK: true}}
	}
	for i := range 8 {
		d := fmt.Sprintf("cdn%d.example", i)
		res[d] = []probe.Result{{URL: probe.SiteURL(d), NoAddress: true}}
	}
	f.probeRes = res
	return f, res
}

// Durum sums the results up instead of listing every site: blocked apps
// first, at most three names, and the rest on the Sites page.
func TestStatusProbeSummary(t *testing.T) {
	defer i18n.Init("en")
	for lang, want := range map[string][]string{
		"en": {"22/35 sites open · 5 blocked · 8 not tested", "2 more; all on the Sites page"},
		"tr": {"22/35 site açılıyor · 5 engelli", "8 test edilmedi", "2 site daha; tümü Siteler sayfasında"},
	} {
		i18n.Init(lang)
		f, _ := manyProbes()
		m := press(t, newModel(t, f), "p")
		v := m.View()
		fits(t, lang+" status", v)
		text := flat(pageText(v))
		for _, w := range append(want,
			"✗ Discord "+i18n.T("probe_short.reset"),
			"✗ Roblox "+i18n.T("probe_short.reset"),
			"✗ a-blocked.example "+i18n.T("probe_short.reset"),
			i18n.T("probe_tui.todo")+" "+probeTodo(probe.Reset),
		) {
			if !strings.Contains(text, w) {
				t.Errorf("%s: %q missing:\n%s", lang, w, v)
			}
		}
		for _, no := range []string{"b-blocked.example", "cdn0.example", "open00.example", "-v"} {
			if strings.Contains(text, no) {
				t.Errorf("%s: %q on the status page:\n%s", lang, no, v)
			}
		}
	}
}

// The TUI's advice points to its own pages, never to command-line flags.
func TestProbeAdviceForTheTUI(t *testing.T) {
	defer i18n.Init("en")
	for _, lang := range []string{"en", "tr", "ru"} {
		i18n.Init(lang)
		for _, c := range []probe.Category{probe.Reset, probe.Timeout, probe.DNS, probe.TLS, probe.HTTP, probe.Other, probe.NoAddress} {
			s := probeTodo(c)
			if s == "" || strings.HasPrefix(s, "!") || strings.Contains(s, "-v") || strings.Contains(s, "sudo") {
				t.Errorf("%s %s: %q", lang, c, s)
			}
		}
		if s, logs := probeTodo(probe.Other), i18n.T("shell.menu.logs"); !strings.Contains(s, logs) {
			t.Errorf("%s other: %q does not point to %s", lang, s, logs)
		}
	}
}

// A site with no address is greyed out on the Sites page, not shown as a failure.
func TestSitesNoAddressMuted(t *testing.T) {
	old := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	lipgloss.SetHasDarkBackground(true)
	defer lipgloss.SetColorProfile(old)
	f, _ := manyProbes()
	f.sites = []string{"a-blocked.example", "cdn0.example"}
	m := goTo(t, press(t, newModel(t, f), "p"), pgSites)
	const mutedFG, badFG = "38;5;244m", "38;5;174m"
	var sawMuted, sawBad bool
	for _, l := range strings.Split(pages[pgSites].View(m, 76, 30), "\n") {
		switch {
		case strings.Contains(l, "cdn0.example"):
			sawMuted = true
			if !strings.Contains(l, mutedFG+"cdn0.example") || strings.Contains(l, badFG) {
				t.Errorf("no-address row not muted: %q", l)
			}
		case strings.Contains(l, "a-blocked.example"):
			sawBad = true
			if !strings.Contains(l, badFG) {
				t.Errorf("blocked row not red: %q", l)
			}
		}
	}
	if !sawMuted || !sawBad {
		t.Errorf("rows missing: muted %v blocked %v", sawMuted, sawBad)
	}
	has(t, flat(ansi.Strip(pages[pgSites].View(m, 76, 30))), probe.NoAddress.What())
}
