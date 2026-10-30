package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/KadirBerkpolat1/SplitDPI/internal/i18n"
	"github.com/KadirBerkpolat1/SplitDPI/internal/ops"
	"github.com/KadirBerkpolat1/SplitDPI/internal/probe"
	"github.com/KadirBerkpolat1/SplitDPI/internal/supervisor"
)

// The busiest Durum at 80x24 gives up blocked-site lines, never the counts,
// what to do, or the hint under them; and no line is cut away.
func TestStatusBusiestFits80x24(t *testing.T) {
	defer i18n.Init("en")
	for _, lang := range []string{"en", "tr", "ru"} {
		i18n.Init(lang)
		f, _ := manyProbes()
		f.foreign = []ops.Foreign{zapret2()}
		f.cfg.DNSProvider = "auto"
		f.st = supervisor.Status{State: "stopped", LastError: "nfqws2 stopped (exit status 1)"}
		m := newModel(t, f)
		m = settle(t, m, m.detectForeign())
		m = press(t, m, "p")
		v := m.View()
		fits(t, lang+" busiest status", v)
		text := flat(pageText(v))
		for _, want := range []string{
			i18n.T("probe_summary.open", 22, 35),
			i18n.T("probe_tui.todo") + " " + probeTodo(probe.Reset),
			i18n.T("background_tui.hint"),
			i18n.T("tui.last_error") + " nfqws2 stopped (exit status 1)",
			"✗ Discord " + i18n.T("probe_short.reset"),
		} {
			if !strings.Contains(text, flat(want)) {
				t.Errorf("%s: %q missing:\n%s", lang, want, v)
			}
		}

		// Everything the page draws is on screen: the frame cuts nothing.
		rows, _ := m.dock()
		h := m.height - 1 - 1 - len(rows) - 1
		width := m.width - menuWidth - 4
		if n := len(strings.Split(statusPage{}.View(m, width, h-4), "\n")); n > h-4 {
			t.Errorf("%s: %d lines for %d rows", lang, n, h-4)
		}

		// With room, all of it shows: three names, the bar, the spacing.
		m, _ = update(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})
		text = flat(pageText100(m.View()))
		for _, want := range []string{"✗ Roblox", "✗ a-blocked.example", strings.Repeat("█", 26), i18n.T("background_tui.hint")} {
			if !strings.Contains(text, flat(want)) {
				t.Errorf("%s 100x30: %q missing:\n%s", lang, want, m.View())
			}
		}

		// Too short for even the smallest fit: an ellipsis says the page goes on.
		lines := strings.Split(statusPage{}.View(m, 60, 6), "\n")
		if len(lines) != 6 || ansi.Strip(lines[5]) != "…" {
			t.Errorf("%s: cut page %q", lang, lines)
		}
	}
}

// pageText100 is the page column of a 100-column screen.
func pageText100(view string) string {
	var out []string
	for _, l := range strings.Split(ansi.Strip(view), "\n") {
		if r := []rune(l); len(r) >= 100 {
			out = append(out, string(r[menuWidth+2:98]))
		}
	}
	return strings.Join(out, "\n")
}
