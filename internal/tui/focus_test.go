package tui

import (
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/KadirBerkpolat1/SplitDPI/internal/i18n"
	"github.com/KadirBerkpolat1/SplitDPI/internal/ops"
	"github.com/KadirBerkpolat1/SplitDPI/internal/presets"
	"github.com/KadirBerkpolat1/SplitDPI/internal/probe"
)

// pageText is the page pane of a full screen: its rows between the borders,
// without the menu column.
func pageText(view string) string {
	var out []string
	for _, l := range strings.Split(ansi.Strip(view), "\n") {
		r := []rune(l)
		if len(r) >= minWidth {
			out = append(out, string(r[menuWidth+2:minWidth-2]))
		}
	}
	return strings.Join(out, "\n")
}

// No app note or failure is cut: in tr and en, with Unicode and ASCII
// glyphs, it wraps under its row on the 80x24 screen.
func TestSitesNotesNotCut(t *testing.T) {
	defer i18n.Init("en")
	for _, lang := range []string{"tr", "en"} {
		for name, g := range map[string]glyphs{"unicode": unicodeGlyphs, "ascii": asciiGlyphs} {
			i18n.Init(lang)
			f := newFake()
			f.set = trSet()
			f.set.Apps["telegram"] = presets.App{ID: "telegram", Name: "Telegram", Note: "Direct connections to Telegram IPs are not covered."}
			f.sites = []string{"a.com", "example.org"}
			f.probeRes = map[string][]probe.Result{"youtube": failedProbe()["discord"]}
			m := newModel(t, f)
			m.g = g
			m = press(t, press(t, m, "p"), "down")
			v := m.View()
			fits(t, lang+" "+name+" sites", v)
			text := flat(pageText(v))
			note := ops.AppNote(f.set.Apps["telegram"])
			if !strings.Contains(text, note) {
				t.Errorf("%s %s: note %q cut:\n%s", lang, name, note, v)
			}
			if what := probe.Classify(failedProbe()["discord"][0]).What(); !strings.Contains(text, what) {
				t.Errorf("%s %s: failure %q cut:\n%s", lang, name, what, v)
			}
			// The rows after the wrapped one keep their places in the list.
			has(t, text, "example.org")
		}
	}
}

// A service whose boot state could not be read still has its start on boot
// turned off, and the confirmation says so.
func TestTakeoverConfirmBootUnknown(t *testing.T) {
	f := newFake()
	z := zapret2()
	z.Service.Enabled, z.Service.BootUnknown = false, true
	f.foreign = []ops.Foreign{z}
	m := newModel(t, f)
	m = press(t, settle(t, m, m.Init()), "z")
	if m.dialog == nil {
		t.Fatal("z did not ask")
	}
	if want := i18n.T("takeover_boot.change", "zapret2"); !slices.Contains(m.dialog.changes, want) {
		t.Errorf("changes %q, want %q", m.dialog.changes, want)
	}
}

// While the menu has focus its cursor is the only one; inside a page, the
// page's list shows its own.
func TestOneCursorWhileMenuFocused(t *testing.T) {
	f := newFake()
	f.set = trSet()
	f.sites = []string{"pastebin.com"}
	f.strategies = testStrategies()
	lists := map[int]bool{pgSites: true, pgStrategy: true, pgScan: true, pgSettings: true}
	for pg := range pages {
		m := goTo(t, newModel(t, f), pg)
		if pg == pgLogs {
			m.stopAll()
		}
		in := strings.Count(m.View(), m.g.cursor)
		m = press(t, m, "esc")
		if n := strings.Count(m.View(), m.g.cursor); n != 0 {
			t.Errorf("%s with the menu focused: %d list cursors, want 0:\n%s", pages[pg].Title(), n, m.View())
		}
		if n := strings.Count(m.View(), m.g.menuMark); n != 1 {
			t.Errorf("%s: %d menu marks, want 1:\n%s", pages[pg].Title(), n, m.View())
		}
		if lists[pg] && in != 1 {
			t.Errorf("%s in the page: %d list cursors, want 1:\n%s", pages[pg].Title(), in, m.View())
		}
	}
}
