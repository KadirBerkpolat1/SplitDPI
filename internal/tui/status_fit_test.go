package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/KadirBerkpolat1/SplitDPI/internal/config"
	"github.com/KadirBerkpolat1/SplitDPI/internal/i18n"
	"github.com/KadirBerkpolat1/SplitDPI/internal/ops"
	"github.com/KadirBerkpolat1/SplitDPI/internal/probe"
	"github.com/KadirBerkpolat1/SplitDPI/internal/supervisor"
)

// The busiest Durum at 80x24 gives up blocked-site lines and the details,
// never the state, the counts or what to do; and no line is cut away.
func TestStatusBusiestFits80x24(t *testing.T) {
	defer i18n.Init("en")
	for _, lang := range []string{"en", "tr", "ru"} {
		i18n.Init(lang)
		f, _ := manyProbes()
		f.foreign = []ops.Foreign{zapret2()}
		f.cfg.DNSProvider = "auto"
		f.cfg.Strategy = &config.Strategy{ID: "tt-multidisorder", Source: "isp/tr/as9121-turk-telekom.toml"}
		f.st = supervisor.Status{State: "stopped", LastError: "nfqws2 stopped (exit status 1)"}
		m := newModel(t, f)
		m = settle(t, m, m.detectForeign())
		m = press(t, press(t, m, "p"), "d")
		v := m.View()
		fits(t, lang+" busiest status", v)
		text := flat(pageText(v))
		for _, want := range []string{
			i18n.T("shell.pill.stopped"),
			i18n.T("tui.how.start_window"),
			i18n.T("probe_summary.open", 22, 35),
			i18n.T("probe_tui.todo") + " " + probeTodo(probe.Reset),
			"✗ Discord " + i18n.T("probe_short.reset"),
		} {
			if !strings.Contains(text, flat(want)) {
				t.Errorf("%s: %q missing:\n%s", lang, want, v)
			}
		}

		// Everything the page draws is on screen: the frame cuts nothing.
		h, width := m.layout().frameH-2, m.width-4
		if n := len(strings.Split(statusPage{}.View(m, width, h), "\n")); n > h {
			t.Errorf("%s: %d lines for %d rows", lang, n, h)
		}

		// With room, all of it shows: three names, the bar, the details.
		m, _ = update(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})
		text = flat(pageText(m.View()))
		for _, want := range []string{"✗ Roblox", "✗ a-blocked.example", strings.Repeat("█", 26), i18n.T("tui.last_error") + " nfqws2 stopped (exit status 1)"} {
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

// The technical details fold away: engine, method id, process and file
// paths show only once d opens them (UX-3).
func TestStatusDetailsFold(t *testing.T) {
	f := newFake()
	f.set = trSet()
	f.cfg.Strategy = &config.Strategy{ID: "tt-multidisorder", Source: "isp/tr/as9121-turk-telekom.toml", ASN: 9121}
	f.st = supervisor.Status{State: "running", Mode: "service", PID: 4242, Engine: "v1.0.6"}
	m := newModel(t, f)
	v := pageText(m.View())
	has(t, v, "Running")
	has(t, v, "Türk Telekom")
	for _, jargon := range []string{"tt-multidisorder", "isp/tr", "zapret2", "nfqws2", "4242", "AS9121", "ASN"} {
		if strings.Contains(v, jargon) {
			t.Errorf("%q on the folded status:\n%s", jargon, v)
		}
	}
	v = pageText(press(t, m, "d").View())
	for _, want := range []string{"tt-multidisorder", "isp/tr/as9121-turk-telekom.toml", "zapret2 v1.0.6", "4242"} {
		has(t, v, want)
	}
}
