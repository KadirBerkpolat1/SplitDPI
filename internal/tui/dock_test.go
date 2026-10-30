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

// dockButton is the dock button of key k.
func dockButton(t *testing.T, m Model, k string) button {
	t.Helper()
	_, btns := m.dock()
	for _, b := range btns {
		if b.key == k {
			return b
		}
	}
	t.Fatalf("no %q button in the dock:\n%s", k, m.View())
	return button{}
}

// dockY is the screen line of a dock row.
func dockY(m Model, row int) int {
	rows, _ := m.dock()
	return m.height - 1 - len(rows) + row
}

func click(t *testing.T, m Model, x, y int) (Model, tea.Cmd) {
	t.Helper()
	return update(t, m, tea.MouseMsg{X: x, Y: y, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
}

// clickKey clicks the last cell of k's button and runs what follows.
func clickKey(t *testing.T, m Model, k string) Model {
	t.Helper()
	b := dockButton(t, m, k)
	m, cmd := click(t, m, b.x+b.width-1, dockY(m, b.row))
	return settle(t, m, cmd)
}

// buttonText is what a button shows in the Unicode glyphs without colors.
func buttonText(k, label string) string { return " " + keyName(k) + " " + label + " " }

// Every page's actions are buttons with their key and whole label, at the
// cells a click finds them on; ? and q end the first row.
func TestDockButtonsEveryPage(t *testing.T) {
	defer i18n.Init("en")
	for _, lang := range []string{"en", "tr", "ru"} {
		i18n.Init(lang)
		f := newFake()
		f.set = trSet()
		f.sites = []string{"pastebin.com"}
		f.strategies = testStrategies()
		f.cfg.DisclaimerAccepted = false
		screens := map[string]Model{"disclaimer": newModel(t, f)}
		f.cfg.DisclaimerAccepted = true
		for pg, p := range pages {
			m := goTo(t, newModel(t, f), pg)
			m.stopAll()
			screens[p.Title()] = m
			screens[p.Title()+" menu"] = press(t, m, "esc")
		}
		screens["sites typing"] = press(t, goTo(t, newModel(t, f), pgSites), "a")
		for name, m := range screens {
			lines := strings.Split(m.View(), "\n")
			fits(t, lang+" "+name, m.View())
			rows, btns := m.dock()
			var want []string
			for _, a := range m.actions() {
				if a.Key != "" {
					want = append(want, a.Key)
				}
			}
			for i, b := range btns {
				line := lines[dockY(m, b.row)]
				if got := ansi.Cut(line, b.x, b.x+b.width); got != buttonText(b.key, b.label) {
					t.Errorf("%s %s: cells %d-%d of %q = %q, want %q", lang, name, b.x, b.x+b.width, line, got, buttonText(b.key, b.label))
				}
				if b.x+b.width > m.width {
					t.Errorf("%s %s: button %q past the edge", lang, name, b.key)
				}
				if !b.global && b.primary != (i == 0) {
					t.Errorf("%s %s: button %d %q primary %v", lang, name, i, b.key, b.primary)
				}
			}
			var keys []string
			for _, b := range btns {
				if !b.global {
					keys = append(keys, b.key)
				}
			}
			if strings.Join(keys, " ") != strings.Join(want, " ") {
				t.Errorf("%s %s: buttons %q, want the actions' keys %q", lang, name, keys, want)
			}
			global := name != "disclaimer" && name != "sites typing"
			if g := strings.HasSuffix(rows[0], buttonText("?", i18n.T("shell.help.key"))+" "+buttonText("q", i18n.T("tui.act.quit"))); g != global {
				t.Errorf("%s %s: ? and q at the right of the first row %v, want %v: %q", lang, name, g, global, rows[0])
			}
		}
	}
}

// Actions that do not fit next to ? and q wrap to a second row, whole; a
// wide terminal takes them all in one.
func TestDockWraps(t *testing.T) {
	f := newFake()
	f.set = trSet()
	m := goTo(t, newModel(t, f), pgSites)
	rows, btns := m.dock()
	if len(rows) != 2 {
		t.Fatalf("%d dock rows at 80 columns, want 2:\n%s", len(rows), strings.Join(rows, "\n"))
	}
	if !strings.HasPrefix(rows[1], buttonText("i", i18n.T("sites_import_tui.act"))) {
		t.Errorf("second row %q", rows[1])
	}
	for _, b := range btns {
		if b.global && b.row != 0 {
			t.Errorf("%q on row %d", b.key, b.row)
		}
	}
	lines := strings.Split(m.View(), "\n")
	if len(lines) != 24 || lines[21] != rows[0] || lines[22] != rows[1] || !strings.HasPrefix(lines[23], "$ ") {
		t.Errorf("dock not on lines 21-22 over the command line:\n%s", m.View())
	}

	m, _ = update(t, m, tea.WindowSizeMsg{Width: 120, Height: 30})
	if rows, _ := m.dock(); len(rows) != 1 || lipgloss.Width(rows[0]) != 120 {
		t.Errorf("at 120 columns: %d rows, first %d wide:\n%s", len(rows), lipgloss.Width(rows[0]), strings.Join(rows, "\n"))
	}
	if lines := strings.Split(m.View(), "\n"); len(lines) != 30 || lipgloss.Width(m.View()) != 120 {
		t.Errorf("120x30: %d lines, %d wide", len(lines), lipgloss.Width(m.View()))
	}
}

func TestDockClickStatus(t *testing.T) {
	// Stop, from the menu.
	f := newFake()
	f.st = supervisor.Status{State: "running", Mode: "service", PID: 7}
	m := clickKey(t, newModel(t, f), "s")
	if !f.called("stop") || !m.inPage {
		t.Errorf("click on s: calls %q, in page %v", f.calls, m.inPage)
	}

	// Run in the background mode starts the background run.
	f = newFake()
	f.cfg.RunMode = config.RunBackground
	m = newModel(t, f)
	r := m.indexOf("r")
	if m = clickKey(t, m, "r"); !f.called("background") || m.focus != r {
		t.Errorf("click on r: calls %q, focus %d, want %d", f.calls, m.focus, r)
	}
	has(t, m.View(), "Running in the background (pid 4343)")

	// Run in this window.
	f = newFake()
	m = newModel(t, f)
	b := dockButton(t, m, "r")
	m, cmd := click(t, m, b.x, dockY(m, b.row))
	done := make(chan struct{})
	go func() { cmd(); close(done) }()
	<-f.runStart
	if !m.running {
		t.Error("click on r did not run the bypass here")
	}
	m.stopRun()
	<-done

	// At boot asks first, as r does.
	f = newFake()
	f.cfg.RunMode = config.RunBoot
	m = clickKey(t, newModel(t, f), "r")
	if m.dialog == nil || f.called("enable") {
		t.Fatalf("click on r at boot: dialog %v, calls %q", m.dialog, f.calls)
	}
	// The dialog takes the clicks' place: the dock does nothing under it.
	if m = clickKey(t, m, "p"); f.called("probe") || m.dialog == nil {
		t.Errorf("a click went past the dialog: calls %q", f.calls)
	}
	if m = press(t, m, "y"); !f.called("enable") {
		t.Errorf("y: calls %q", f.calls)
	}

	f = newFake()
	if clickKey(t, newModel(t, f), "p"); !f.called("probe") {
		t.Errorf("click on p: calls %q", f.calls)
	}
}

// indexOf is the position of key k among the page's actions.
func (m Model) indexOf(k string) int {
	for i, a := range m.actions() {
		if a.Key == k {
			return i
		}
	}
	return -1
}

func TestDockClickSitesAdd(t *testing.T) {
	f := newFake()
	f.set = trSet()
	m := press(t, newModel(t, f), "down") // the menu has focus
	m = clickKey(t, m, "a")
	if !m.apps.typing || !m.inPage {
		t.Fatalf("click on a: typing %v, in page %v", m.apps.typing, m.inPage)
	}
	m = clickKey(t, typeText(t, m, "example.com"), "enter")
	if !f.called("sites add example.com") || m.apps.typing {
		t.Fatalf("click on enter: calls %q, typing %v", f.calls, m.apps.typing)
	}
	has(t, m.View(), "example.com")

	// esc cancels, as the key does.
	m = clickKey(t, clickKey(t, m, "a"), "esc")
	if m.apps.typing || !m.inPage {
		t.Errorf("click on esc: typing %v, in page %v", m.apps.typing, m.inPage)
	}
}

func TestDockClickSettingsChange(t *testing.T) {
	defer i18n.Init("en")
	f := newFake()
	m := press(t, toSetting(t, f, "lang"), "esc") // the menu has focus
	m = clickKey(t, m, "enter")
	if p := m.settings.pick; p == nil || p.id != "lang" || !m.inPage {
		t.Fatalf("click on enter: picker %+v, in page %v", p, m.inPage)
	}
	m = clickKey(t, press(t, m, "down"), "enter")
	if !f.called("lang tr") || m.settings.pick != nil {
		t.Fatalf("click on Choose: calls %q", f.calls)
	}
	has(t, m.View(), "Dil kaydedildi.")
}

func TestPillClick(t *testing.T) {
	pillX := func(m Model) int { return m.width - lipgloss.Width(m.pill()) }

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
	f.cfg.RunMode = config.RunBackground
	m = goTo(t, newModel(t, f), pgSettings)
	m, cmd = click(t, m, m.width-1, 0)
	if m = settle(t, m, cmd); !f.called("background") || m.page != pgSettings {
		t.Errorf("click on the stopped pill: calls %q, page %d", f.calls, m.page)
	}

	f = newFake()
	f.cfg.RunMode = config.RunBoot
	m = newModel(t, f)
	m.g = asciiGlyphs
	m, cmd = click(t, m, pillX(m), 0)
	if m = settle(t, m, cmd); m.dialog == nil || f.called("enable") {
		t.Errorf("click on the pill at boot: dialog %v, calls %q", m.dialog, f.calls)
	}

	f = newFake()
	m = newModel(t, f)
	m.bgStarting = true
	m, cmd = click(t, m, m.width-1, 0)
	if settle(t, m, cmd); len(f.calls) != 0 {
		t.Errorf("click on the pill while starting: calls %q", f.calls)
	}
}

// The note sits on the line over the dock, wrapped, and the line is blank
// without one.
func TestToast(t *testing.T) {
	m := newModel(t, newFake())
	toastY := func(m Model) int { return dockY(m, 0) - 1 }
	if l := strings.Split(m.View(), "\n")[toastY(m)]; strings.TrimSpace(l) != "" {
		t.Errorf("toast without a note: %q", l)
	}
	m.note = "Strategy set."
	if l := strings.Split(m.View(), "\n")[toastY(m)]; strings.TrimSpace(l) != "Strategy set." {
		t.Errorf("toast %q", l)
	}
	m.note = strings.Repeat("a long result that wraps ", 6)
	lines := strings.Split(m.View(), "\n")
	fits(t, "long toast", m.View())
	if got := flat(lines[toastY(m)-1] + " " + lines[toastY(m)]); got != strings.TrimSpace(m.note) {
		t.Errorf("long toast %q", got)
	}
}

func TestDockASCII(t *testing.T) {
	f, _ := manyProbes()
	f.st = supervisor.Status{State: "running", Mode: "service", PID: 7}
	f.cfg.Strategy = &config.Strategy{ID: "tt-multidisorder"}
	m := newModel(t, f)
	m.g = asciiGlyphs
	m = press(t, m, "p")
	v := m.View()
	fits(t, "ascii", v)
	lines := strings.Split(v, "\n")
	if !strings.HasSuffix(lines[0], " * RUNNING") {
		t.Errorf("header %q", lines[0])
	}
	rows, _ := m.dock()
	left, right := "[s "+i18n.T("tui.act.stop")+"] [p "+i18n.T("tui.act.probe")+"] ", " [? Help] [q Quit]"
	if len(rows) != 1 || !strings.HasPrefix(rows[0], left) || !strings.HasSuffix(rows[0], right) || len(rows[0]) != 80 {
		t.Errorf("ascii dock %q", rows)
	}
	has(t, v, strings.Repeat("#", 26)+strings.Repeat("x", 5)+strings.Repeat("-", 9))
	if m = clickKey(t, m, "s"); !f.called("stop") {
		t.Errorf("click on [s Stop]: calls %q", f.calls)
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

// The help and the dialogs cover the page, not the toast, dock or command
// line.
func TestOverlaysAboveDock(t *testing.T) {
	f := newFake()
	f.cfg.RunMode = config.RunBoot
	f.cfg.Strategy = &config.Strategy{ID: "tt-multidisorder"} // r first, so it keeps the focus
	m := newModel(t, f)
	m.note = "A note."
	bottom := func(m Model) string {
		lines := strings.Split(m.View(), "\n")
		return strings.Join(lines[dockY(m, 0)-1:], "\n")
	}
	want := bottom(m)
	help, dialog := press(t, m, "?"), press(t, m, "r")
	for name, c := range map[string]struct {
		m   Model
		box string
	}{"help": {help, help.helpView()}, "dialog": {dialog, dialog.dialogView()}} {
		v := c.m.View()
		fits(t, name, v)
		if got := bottom(c.m); got != want {
			t.Errorf("%s changed the bottom:\n%s\nwant:\n%s", name, got, want)
		}
		if !wholeBox(v, c.box) {
			t.Errorf("%s cut off:\n%s", name, v)
		}
	}
	// Under the help a click does nothing.
	if m = clickKey(t, press(t, m, "?"), "q"); !m.help || m.quitting {
		t.Errorf("a click went past the help: help %v", m.help)
	}

	// The help is whole over every page, however many rows the dock takes;
	// with a long note it covers the toast, but not the dock.
	defer i18n.Init("en")
	for _, lang := range []string{"en", "tr", "ru"} {
		i18n.Init(lang)
		f := newFake()
		f.set = trSet()
		f.foreign = []ops.Foreign{zapret2()}
		for pg, p := range pages {
			m := goTo(t, newModel(t, f), pg)
			m = settle(t, m, m.detectForeign())
			m.stopAll()
			for _, note := range []string{"", strings.Repeat("a note that takes two lines ", 4)} {
				m.note = note
				dock := bottom(m)[strings.Index(bottom(m), "\n"):]
				h := press(t, m, "?")
				v := h.View()
				fits(t, lang+" "+p.Title()+" help", v)
				if !wholeBox(v, h.helpView()) {
					t.Errorf("%s %s: help cut off:\n%s", lang, p.Title(), v)
				}
				if !strings.HasSuffix(v, dock) {
					t.Errorf("%s %s: help covers the dock:\n%s", lang, p.Title(), v)
				}
			}
		}
	}
}

// wholeBox is whether every line of an overlay box shows in view, one under
// the other at the box's column.
func wholeBox(view, box string) bool {
	lines, over := strings.Split(ansi.Strip(view), "\n"), strings.Split(ansi.Strip(box), "\n")
	x := menuWidth + max((minWidth-menuWidth-lipgloss.Width(box))/2, 0)
	at := func(y int) string { return ansi.Cut(lines[y], x, x+lipgloss.Width(box)) }
	for y := range len(lines) - len(over) + 1 {
		whole := true
		for i, o := range over {
			whole = whole && at(y+i) == o
		}
		if whole {
			return true
		}
	}
	return false
}

func TestDockClickDisclaimer(t *testing.T) {
	f := newFake()
	f.cfg.DisclaimerAccepted = false
	m := newModel(t, f)
	if m = clickKey(t, m, "enter"); !f.called("accept disclaimer") || m.showDisclaimer() {
		t.Errorf("click on accept: calls %q", f.calls)
	}
}
