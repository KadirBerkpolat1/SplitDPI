package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/KadirBerkpolat1/SplitDPI/internal/config"
	"github.com/KadirBerkpolat1/SplitDPI/internal/i18n"
	"github.com/KadirBerkpolat1/SplitDPI/internal/ops"
	"github.com/KadirBerkpolat1/SplitDPI/internal/supervisor"
)

// barSpan is the hint bar's span of key k.
func barSpan(t *testing.T, m Model, k string) span {
	t.Helper()
	_, spans := m.hintBar()
	for _, s := range spans {
		if s.key == k {
			return s
		}
	}
	t.Fatalf("no %q in the hint bar:\n%s", k, m.View())
	return span{}
}

func click(t *testing.T, m Model, x, y int) (Model, tea.Cmd) {
	t.Helper()
	return update(t, m, tea.MouseMsg{X: x, Y: y, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
}

// clickKey clicks the last cell of k in the hint bar and runs what follows.
func clickKey(t *testing.T, m Model, k string) Model {
	t.Helper()
	s := barSpan(t, m, k)
	m, cmd := click(t, m, s.x1-1, s.y)
	return settle(t, m, cmd)
}

// clickPopup clicks the popup's span of key k and runs what follows.
func clickPopup(t *testing.T, m Model, k string) Model {
	t.Helper()
	spans, _, ok := m.popupSpans()
	if !ok {
		t.Fatalf("no popup:\n%s", m.View())
	}
	for _, s := range spans {
		if s.key == k {
			m, cmd := click(t, m, s.x0, s.y)
			return settle(t, m, cmd)
		}
	}
	t.Fatalf("no %q in the popup:\n%s", k, m.View())
	return m
}

// Every screen ends in one line of key hints that never wraps at 80
// columns: the screen's first keys in order, each whole at the cells a click
// finds it on, at most maxHints of them, then ? and q at the right.
func TestHintBarEveryScreen(t *testing.T) {
	defer i18n.Init("en")
	for _, lang := range i18n.Supported() {
		i18n.Init(lang)
		views := map[string]Model{}
		f := newFake()
		f.set = trSet()
		f.sites = []string{"pastebin.com"}
		f.excluded = []string{"bank.example"}
		f.strategies = testStrategies()
		f.cfg.DisclaimerAccepted = false
		views["disclaimer"] = newModel(t, f)
		f.cfg.DisclaimerAccepted = true
		for pg, p := range pages {
			m := goTo(t, newModel(t, f), pg)
			m.stopAll()
			views[p.Title()] = m
		}
		views["sites typing"] = press(t, goTo(t, newModel(t, f), pgSites), "a")
		views["sites excluded"] = press(t, goTo(t, newModel(t, f), pgSites), "up")
		views["app editor"] = press(t, goTo(t, newModel(t, f), pgSites), "o")
		views["strategy"] = press(t, goTo(t, newModel(t, f), pgSetup), "m")
		views["apps step"] = press(t, newModel(t, f), "w")
		f.st = supervisor.Status{State: "running", Mode: "background", PID: 7}
		f.cfg.Strategy = &config.Strategy{ID: "tt-multidisorder"}
		pend := newModel(t, f)
		pend.pending = true
		views["status pending"] = pend
		for _, id := range []string{"lang", "run_mode", "dns", "mode", "skip_ifaces", "qnum", "doctor"} {
			views["settings "+id] = press(t, toSetting(t, f, id), "enter")
		}

		for name, m := range views {
			v := m.View()
			fits(t, lang+" "+name, v)
			lines := strings.Split(v, "\n")
			bar, spans := m.hintBar()
			if strings.Contains(bar, "\n") || lipgloss.Width(bar) > m.width || lines[len(lines)-1] != bar {
				t.Errorf("%s %s: hint bar %q is not the last line, alone", lang, name, bar)
			}
			left := 0
			for _, s := range spans {
				if got := ansi.Strip(ansi.Cut(bar, s.x0, s.x1)); !strings.HasPrefix(got, keyName(s.key)+" ") {
					t.Errorf("%s %s: cells %d-%d of %q = %q, want key %q", lang, name, s.x0, s.x1, bar, got, s.key)
				}
				if s.key != "?" && s.key != "q" {
					left++
				}
			}
			if left == 0 && name != "Logs" && name != i18n.T("shell.menu.logs") {
				t.Errorf("%s %s: no keys in %q", lang, name, bar)
			}
			if left > maxHints {
				t.Errorf("%s %s: %d keys in %q", lang, name, left, bar)
			}
			var first string
			for _, a := range m.actions() {
				if a.Key != "" {
					first = a.Key
					break
				}
			}
			if m.pending {
				first = restartKey
			}
			if first != "" && (len(spans) == 0 || spans[0].key != first) {
				t.Errorf("%s %s: the bar %q does not start with %q", lang, name, bar, first)
			}
			global := name != "disclaimer" && name != "sites typing" && name != "settings qnum"
			right := hint("?", i18n.T("shell.help.key")) + "  " + hint("q", i18n.T("tui.act.quit"))
			if g := strings.HasSuffix(bar, right); g != global {
				t.Errorf("%s %s: ? and q at the right %v, want %v: %q", lang, name, g, global, bar)
			}
		}
	}
}

func TestHintBarClickStatus(t *testing.T) {
	f := newFake()
	f.st = supervisor.Status{State: "running", Mode: "service", PID: 7}
	f.cfg.Strategy = &config.Strategy{ID: "tt-multidisorder"}
	if clickKey(t, newModel(t, f), "s"); !f.called("stop") {
		t.Errorf("click on s: calls %q", f.calls)
	}

	// Start in the background mode starts the background run.
	f = newFake()
	f.cfg.Strategy = &config.Strategy{ID: "tt-multidisorder"}
	f.cfg.RunMode = config.RunBackground
	m := clickKey(t, newModel(t, f), "r")
	if !f.called("background") {
		t.Errorf("click on r: calls %q", f.calls)
	}
	has(t, m.View(), "Running in the background (pid 4343)")

	// Start in this window.
	f = newFake()
	f.cfg.Strategy = &config.Strategy{ID: "tt-multidisorder"}
	m = newModel(t, f)
	s := barSpan(t, m, "r")
	m, cmd := click(t, m, s.x0, s.y)
	done := make(chan struct{})
	go func() { cmd(); close(done) }()
	<-f.runStart
	if !m.running {
		t.Error("click on r did not run the bypass here")
	}
	m.stopRun()
	<-done

	// At boot asks first, as r does; the dialog takes the clicks.
	f = newFake()
	f.cfg.Strategy = &config.Strategy{ID: "tt-multidisorder"}
	f.cfg.RunMode = config.RunBoot
	m = newModel(t, f)
	p := barSpan(t, m, "p")
	m = clickKey(t, m, "r")
	if m.dialog == nil || f.called("enable") {
		t.Fatalf("click on r at boot: dialog %v, calls %q", m.dialog, f.calls)
	}
	if m, cmd = click(t, m, p.x0, p.y); f.called("probe") || m.dialog == nil {
		t.Errorf("a click went past the dialog: calls %q", f.calls)
	}
	// A click on the dialog's yes answers it (TUI-10).
	if m = clickPopup(t, settle(t, m, cmd), i18n.T("confirm_keys.yes")); !f.called("enable") || m.dialog != nil {
		t.Errorf("click on yes: calls %q", f.calls)
	}

	f = newFake()
	if clickKey(t, newModel(t, f), "p"); !f.called("probe") {
		t.Errorf("click on p: calls %q", f.calls)
	}
}

func TestHintBarClickSitesAdd(t *testing.T) {
	f := newFake()
	f.set = trSet()
	m := clickKey(t, goTo(t, newModel(t, f), pgSites), "a")
	if !m.apps.typing {
		t.Fatal("click on a did not open the input")
	}
	m = clickKey(t, typeText(t, m, "example.com"), "enter")
	if !f.called("sites add example.com") || m.apps.typing {
		t.Fatalf("click on enter: calls %q, typing %v", f.calls, m.apps.typing)
	}
	has(t, m.View(), "example.com")

	// esc cancels, as the key does.
	if m = clickKey(t, clickKey(t, m, "a"), "esc"); m.apps.typing || m.page != pgSites {
		t.Errorf("click on esc: typing %v, page %d", m.apps.typing, m.page)
	}
}

func TestHintBarClickSettingsChange(t *testing.T) {
	defer i18n.Init("en")
	f := newFake()
	m := clickKey(t, toSetting(t, f, "lang"), "enter")
	if p := m.settings.pick; p == nil || p.id != "lang" {
		t.Fatalf("click on enter: picker %+v", p)
	}
	m = clickKey(t, press(t, m, "down"), "enter")
	if !f.called("lang tr") || m.settings.pick != nil {
		t.Fatalf("click on Choose: calls %q", f.calls)
	}
	has(t, m.View(), "Dil kaydedildi.")
}

func TestPillClick(t *testing.T) {
	pillX := func(m Model) int { return m.width - lipgloss.Width(m.pill(true)) }

	f := newFake()
	f.st = supervisor.Status{State: "running", Mode: "service", PID: 7}
	m := newModel(t, f)
	m, cmd := click(t, m, pillX(m)-1, 0)
	if m = settle(t, m, cmd); f.called("stop") {
		t.Fatal("a click left of the pill stopped the bypass")
	}
	for _, x := range []int{pillX(m), m.width - 1} {
		f.calls = nil
		m, cmd = click(t, m, x, 0)
		if m = settle(t, m, cmd); !f.called("stop") || m.page != pgStatus {
			t.Errorf("click on the pill at %d: calls %q", x, f.calls)
		}
	}

	f = newFake()
	f.cfg.Strategy = &config.Strategy{ID: "tt-multidisorder"}
	f.cfg.RunMode = config.RunBackground
	m = goTo(t, newModel(t, f), pgSettings)
	m, cmd = click(t, m, m.width-1, 0)
	if m = settle(t, m, cmd); !f.called("background") || m.page != pgSettings {
		t.Errorf("click on the stopped pill: calls %q, page %d", f.calls, m.page)
	}

	f = newFake()
	f.cfg.Strategy = &config.Strategy{ID: "tt-multidisorder"}
	f.cfg.RunMode = config.RunBoot
	m = newModel(t, f)
	m.g = asciiGlyphs
	m, cmd = click(t, m, pillX(m), 0)
	if m = settle(t, m, cmd); m.dialog == nil || f.called("enable") {
		t.Errorf("click on the pill at boot: dialog %v, calls %q", m.dialog, f.calls)
	}

	// Nothing to start without a method, or while a start is under way.
	for _, starting := range []bool{false, true} {
		f = newFake()
		if starting {
			f.cfg.Strategy = &config.Strategy{ID: "tt-multidisorder"}
		}
		m = newModel(t, f)
		m.bgStarting = starting
		m, cmd = click(t, m, m.width-1, 0)
		if settle(t, m, cmd); len(f.calls) != 0 {
			t.Errorf("click on the pill, starting %v: calls %q", starting, f.calls)
		}
	}
}

// The note sits over the hint bar, wrapped; without one the frame takes its
// line.
func TestToast(t *testing.T) {
	m := newModel(t, newFake())
	lines := strings.Split(m.View(), "\n")
	if !strings.HasPrefix(lines[m.height-2], "╰") {
		t.Errorf("without a note the frame ends over the hint bar: %q", lines[m.height-2])
	}
	m.note = "Method set."
	if l := strings.Split(m.View(), "\n")[m.height-2]; strings.TrimSpace(l) != "Method set." {
		t.Errorf("toast %q", l)
	}
	m.note = strings.Repeat("a long result that wraps ", 6)
	lines = strings.Split(m.View(), "\n")
	fits(t, "long toast", m.View())
	if got := flat(lines[m.height-3] + " " + lines[m.height-2]); got != strings.TrimSpace(m.note) {
		t.Errorf("long toast %q", got)
	}
}

func TestHintBarASCII(t *testing.T) {
	f, _ := manyProbes()
	f.st = supervisor.Status{State: "running", Mode: "service", PID: 7}
	f.cfg.Strategy = &config.Strategy{ID: "tt-multidisorder"}
	m := newModel(t, f)
	m.g = asciiGlyphs
	m = press(t, m, "p")
	v := m.View()
	fits(t, "ascii", v)
	lines := strings.Split(v, "\n")
	if !strings.HasSuffix(lines[0], "* Running") || !strings.Contains(lines[0], "[1 Status]") {
		t.Errorf("header %q", lines[0])
	}
	bar, _ := m.hintBar()
	if !strings.HasPrefix(bar, " s "+i18n.T("tui.act.stop")+"  ") || !strings.HasSuffix(bar, "? Help  q Quit") {
		t.Errorf("ascii hint bar %q", bar)
	}
	has(t, v, strings.Repeat("#", 26)+strings.Repeat("x", 5)+strings.Repeat("-", 9))
	if clickKey(t, m, "s"); !f.called("stop") {
		t.Errorf("click on s Stop: calls %q", f.calls)
	}
}

func TestReachBar(t *testing.T) {
	for _, c := range []struct {
		counts [3]int
		width  int
		want   [3]int
	}{
		{[3]int{22, 5, 8}, 40, [3]int{26, 5, 9}},
		{[3]int{1, 0, 99}, 40, [3]int{1, 0, 39}},
		{[3]int{3, 0, 0}, 10, [3]int{10, 0, 0}},
		{[3]int{1, 1, 1}, 40, [3]int{14, 13, 13}},
		{[3]int{0, 0, 0}, 40, [3]int{}},
	} {
		if got := barCells(c.counts, c.width); got != c.want {
			t.Errorf("barCells(%v, %d) = %v, want %v", c.counts, c.width, got, c.want)
		}
	}

	defer i18n.Init("en")
	for _, lang := range []string{"en", "tr", "ru"} {
		i18n.Init(lang)
		f, _ := manyProbes()
		lines := strings.Split(press(t, newModel(t, f), "p").View(), "\n")
		bar := strings.Repeat("█", 26) + strings.Repeat("▓", 5) + strings.Repeat("░", 9)
		for i, l := range lines {
			if strings.Contains(l, bar) {
				if !strings.Contains(l, i18n.T("tui.probe")) || !strings.Contains(lines[i+1], i18n.T("probe_summary.open", 22, 35)) {
					t.Errorf("%s: bar line %q, then %q", lang, l, lines[i+1])
				}
				break
			}
			if i == len(lines)-1 {
				t.Errorf("%s: no bar:\n%s", lang, strings.Join(lines, "\n"))
			}
		}
	}
}

// The popups cover the page, not the tab bar or the hint bar, and are whole
// over every page in every language, a long note or not.
func TestPopupsBetweenBars(t *testing.T) {
	defer i18n.Init("en")
	for _, lang := range i18n.Supported() {
		i18n.Init(lang)
		f := newFake()
		f.set = trSet()
		f.foreign = []ops.Foreign{zapret2()}
		f.cfg.Strategy = &config.Strategy{ID: "tt-multidisorder"}
		f.cfg.RunMode = config.RunBoot
		for pg, p := range pages {
			m := goTo(t, newModel(t, f), pg)
			m = settle(t, m, m.detectForeign())
			m.stopAll()
			for _, note := range []string{"", strings.Repeat("a note that takes two lines ", 4)} {
				m.note = note
				base := strings.Split(m.View(), "\n")
				popups := map[string]Model{"help": press(t, m, "?")}
				if pg == pgStatus {
					popups["dialog"] = press(t, m, "r")
				}
				q := m
				q.running, q.confirmQuit = true, true
				popups["quit"] = q
				for name, pm := range popups {
					v := pm.View()
					fits(t, lang+" "+p.Title()+" "+name, v)
					lines := strings.Split(v, "\n")
					if lines[len(lines)-1] != base[len(base)-1] && name != "quit" {
						t.Errorf("%s %s %s covers the hint bar:\n%s", lang, p.Title(), name, v)
					}
					box, _, ok := pm.popupSpans()
					if !ok || len(box) == 0 {
						t.Errorf("%s %s %s: no keys to click", lang, p.Title(), name)
						continue
					}
					last := box[len(box)-1]
					if last.y <= 0 || last.y >= pm.height-2 || !strings.Contains(lines[last.y+1], "╰") {
						t.Errorf("%s %s %s cut off at its keys (line %d):\n%s", lang, p.Title(), name, last.y, v)
					}
				}
			}
		}
	}
}

// A click beside the help closes it, and one on its close key too; under
// the quit prompt a click on an answer answers (TUI-10).
func TestPopupClicks(t *testing.T) {
	m := press(t, newModel(t, newFake()), "?")
	if m = clickPopup(t, m, "esc"); m.help {
		t.Error("a click on Close did not close the help")
	}
	m = press(t, m, "?")
	if m, _ = click(t, m, 0, 3); m.help {
		t.Error("a click beside the help did not close it")
	}

	q := newModel(t, newFake())
	q.running, q.confirmQuit = true, true
	if q = clickPopup(t, q, i18n.T("confirm_keys.no")); q.confirmQuit || q.quitting {
		t.Errorf("a click on Cancel: prompt %v quitting %v", q.confirmQuit, q.quitting)
	}
}

func TestHintBarClickDisclaimer(t *testing.T) {
	f := newFake()
	f.cfg.DisclaimerAccepted = false
	m := newModel(t, f)
	if m = clickKey(t, m, "enter"); !f.called("accept disclaimer") || m.showDisclaimer() {
		t.Errorf("click on accept: calls %q", f.calls)
	}
}
