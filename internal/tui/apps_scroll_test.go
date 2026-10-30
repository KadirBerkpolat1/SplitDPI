package tui

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/KadirBerkpolat1/SplitDPI/internal/i18n"
	"github.com/KadirBerkpolat1/SplitDPI/internal/presets"
	"github.com/KadirBerkpolat1/SplitDPI/internal/scan"
)

func TestScrollMore(t *testing.T) {
	m := newModel(t, newFake())
	lines := make([]string, 20)
	for i := range lines {
		lines[i] = fmt.Sprintf("row %d", i)
	}
	for _, tc := range []struct {
		cursor       int
		above, below string
	}{
		{0, "", "↓ 14 more"},
		{10, "↑ 8 more", "↓ 7 more"},
		{19, "↑ 14 more", ""},
	} {
		out := m.scrollMore(lines, nil, tc.cursor, 7, 80)
		if len(out) != 7 || !slices.Contains(out, fmt.Sprintf("row %d", tc.cursor)) {
			t.Errorf("cursor %d: %q; want 7 lines with the cursor row", tc.cursor, out)
		}
		if got := strings.TrimSpace(out[0]); tc.above != "" && got != tc.above {
			t.Errorf("cursor %d: first line %q, want %q", tc.cursor, got, tc.above)
		}
		if got := strings.TrimSpace(out[6]); tc.below != "" && got != tc.below {
			t.Errorf("cursor %d: last line %q, want %q", tc.cursor, got, tc.below)
		}
	}
	// Items of several lines count once each, and the window starts on an item.
	first := []int{0, 3, 6, 9, 12, 15, 18}
	out := m.scrollMore(lines, first, 2, 7, 80)
	if len(out) != 7 || strings.TrimSpace(out[0]) != "↑ 2 more" || out[1] != "row 6" || strings.TrimSpace(out[6]) != "↓ 3 more" {
		t.Errorf("multi-line items: %q", out)
	}
	if got := m.scrollMore(lines[:5], nil, 4, 7, 80); len(got) != 5 {
		t.Errorf("a list that fits: %q", got)
	}
}

// cursorLine is the view's line with the list cursor, if any.
func cursorLine(v string) string {
	for l := range strings.SplitSeq(v, "\n") {
		if strings.Contains(l, unicodeGlyphs.cursor+" ") {
			return l
		}
	}
	return ""
}

// walk moves the cursor down over every app, the last row being the last
// app, and checks that each view fits with the cursor row and the right
// "n more" indicators in sight; up from the first row wraps to the last.
func walk(t *testing.T, name string, m Model, apps func(Model) []presets.App, cursor func(Model) int) {
	t.Helper()
	list := apps(m)
	check := func(m Model, i int) {
		t.Helper()
		v := m.View()
		fits(t, fmt.Sprintf("%s row %d", name, i), v)
		cur := cursorLine(v)
		if cursor(m) != i || !strings.Contains(cur, list[i].Name) {
			t.Fatalf("%s row %d: cursor %d on %q, want %s:\n%s", name, i, cursor(m), cur, list[i].Name, v)
		}
		up, down := strings.Contains(v, unicodeGlyphs.up+" "), strings.Contains(v, unicodeGlyphs.down+" ")
		switch i {
		case 0:
			if up || !down {
				t.Errorf("%s first row: want only the ↓ indicator:\n%s", name, v)
			}
		case len(list) - 1:
			if !up || down {
				t.Errorf("%s last row: want only the ↑ indicator:\n%s", name, v)
			}
		}
	}
	for i := range list {
		check(m, i)
		if i < len(list)-1 {
			m = press(t, m, "down")
		}
	}
	m = press(t, press(t, m, "down"), "up")
	check(m, len(list)-1)
}

func TestManyAppsFit80x24(t *testing.T) {
	defer i18n.Init("en")
	set := presets.Load(presets.Embedded())
	if len(set.Apps) < 14 {
		t.Fatalf("%d embedded apps, want 14", len(set.Apps))
	}
	for _, lang := range []string{"tr", "en", "ru"} {
		i18n.Init(lang)
		f := newFake()
		f.set = set
		f.cfg.Apps = []string{"youtube", "x"}
		f.detected = scan.ASNInfo{ASN: 9121, Country: "TR", Org: "Turk Telekom"}

		s := goTo(t, newModel(t, f), pgSites)
		if got := []string{s.apps.apps[0].ID, s.apps.apps[1].ID}; !slices.Equal(got, []string{"x", "youtube"}) {
			t.Errorf("%s sites: first apps %v, want the enabled ones", lang, got)
		}
		walk(t, lang+" sites", s,
			func(m Model) []presets.App { return m.apps.apps },
			func(m Model) int { return m.apps.cursor })

		w := press(t, press(t, newModel(t, f), "w"), "y")
		if w.wiz.step != wApps {
			t.Fatalf("%s: wizard step %v, want the apps step", lang, w.wiz.step)
		}
		walk(t, lang+" wizard apps", w,
			func(m Model) []presets.App { return m.wiz.apps },
			func(m Model) int { return m.wiz.cursor })
	}
}
