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

// pageText is the page in the frame of a full screen: its rows between the
// side borders.
func pageText(view string) string {
	var out []string
	for _, l := range strings.Split(ansi.Strip(view), "\n") {
		r := []rune(l)
		if len(r) >= minWidth && (r[0] == '│' || r[0] == '|') {
			out = append(out, string(r[2:len(r)-2]))
		}
	}
	return strings.Join(out, "\n")
}

// cursorOn moves the Sites cursor down to the app id.
func cursorOn(t *testing.T, m Model, id string) Model {
	t.Helper()
	for range m.apps.rows() {
		if k, i := m.apps.at(m.apps.cursor); k == rowApp && m.apps.apps[i].ID == id {
			return m
		}
		m = press(t, m, "down")
	}
	t.Fatalf("no app %s", id)
	return m
}

// No app note or what to do about a failure is cut: in tr and en, with
// Unicode and ASCII glyphs, it wraps under the list on the 80x24 screen.
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
			m = goTo(t, press(t, m, "p"), pgSites)

			v := cursorOn(t, m, "telegram").View()
			fits(t, lang+" "+name+" sites", v)
			if note := ops.AppNote(f.set.Apps["telegram"]); !strings.Contains(flat(pageText(v)), note) {
				t.Errorf("%s %s: note %q cut:\n%s", lang, name, note, v)
			}
			v = cursorOn(t, m, "youtube").View()
			text := flat(pageText(v))
			if todo := probeTodo(probe.Reset); !strings.Contains(text, todo) {
				t.Errorf("%s %s: what to do %q cut:\n%s", lang, name, todo, v)
			}
			// The rows after the selected one keep their places in the list.
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

// Each page with a list shows one cursor, at once: there is no stage of
// entering the page first.
func TestOneCursorPerPage(t *testing.T) {
	f := newFake()
	f.set = trSet()
	f.sites = []string{"pastebin.com"}
	f.strategies = testStrategies()
	lists := map[int]bool{pgSites: true, pgSetup: true, pgSettings: true}
	for pg := range pages {
		m := goTo(t, newModel(t, f), pg)
		if pg == pgLogs {
			m.stopAll()
		}
		want := 0
		if lists[pg] {
			want = 1
		}
		if n := strings.Count(m.View(), m.g.cursor); n != want {
			t.Errorf("%s: %d list cursors, want %d:\n%s", pages[pg].Title(), n, want, m.View())
		}
	}
}
