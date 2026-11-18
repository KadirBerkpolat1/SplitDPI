package tui

import (
	"context"
	"os"
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/KadirBerkpolat1/SplitDPI/internal/config"
	"github.com/KadirBerkpolat1/SplitDPI/internal/i18n"
	"github.com/KadirBerkpolat1/SplitDPI/internal/scan"
	"github.com/KadirBerkpolat1/SplitDPI/internal/supervisor"
)

// pillClick clicks the pill.
func pillClick(t *testing.T, m Model) (Model, tea.Cmd) {
	t.Helper()
	for _, sp := range m.layout().headSpans {
		if sp.key == pillKey {
			return update(t, m, tea.MouseMsg{X: sp.x0, Y: sp.y, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
		}
	}
	t.Fatal("no pill in the header")
	return m, nil
}

func actionKeys(acts []action) []string {
	var keys []string
	for _, a := range acts {
		keys = append(keys, a.Key)
	}
	return keys
}

// A-4: while a setup scan runs, the pill and Status show the scan and
// neither stop the trial (the TUI itself) nor start the bypass mid-scan, and
// R restarts nothing.
func TestScanHoldsStartStop(t *testing.T) {
	f := newFake()
	m := scanningUnder(t, f, context.Background())
	f.mu.Lock()
	f.cfg.Strategy = &config.Strategy{ID: "tt-multidisorder"} // a setup scan of an installed machine
	f.mu.Unlock()
	defer func() { m.wiz.scanCancel(); m.wiz.done.wait() }()
	before := len(f.calls)
	for _, st := range []supervisor.Status{
		{State: "running", Mode: "scan", PID: os.Getpid()}, // a trial
		{State: "stopped"}, // between trials
	} {
		f.mu.Lock()
		f.st = st
		f.mu.Unlock()
		m, _ = update(t, m, readStatus(f))
		m.pending = true
		if strings.Contains(ansi.Strip(m.pill(true)), i18n.T("shell.pill.running")) || !strings.Contains(ansi.Strip(m.pill(true)), i18n.T("fix_arayuz.pill_scanning")) {
			t.Errorf("%s: pill %q, want the scan", st.State, ansi.Strip(m.pill(true)))
		}
		if a, ok := m.pillAction(); ok {
			t.Errorf("%s: the pill offers %q during the scan", st.State, a.Key)
		}
		m = settle(t, m, func() tea.Cmd { _, c := pillClick(t, m); return c }())
		if keys := actionKeys(statusPage{}.Actions(m)); slices.Contains(keys, "s") || slices.Contains(keys, "r") || slices.Contains(keys, "b") {
			t.Errorf("%s: Status offers %q during the scan", st.State, keys)
		}
		if _, ok := m.restartAction(); ok {
			t.Errorf("%s: R offered during the scan", st.State)
		}
		m = press(t, m, restartKey)
		sm := goTo(t, m, pgStatus)
		has(t, sm.View(), i18n.T("tui.how.scan"))
		sm = press(t, press(t, sm, "r"), "s")
		if strings.Contains(sm.View(), i18n.T("tui.pending", restartKey)) {
			t.Errorf("%s: Status asks for R during the scan", st.State)
		}
	}
	f.mu.Lock()
	calls := slices.Clone(f.calls[before:])
	f.mu.Unlock()
	for _, c := range calls {
		if c == "stop" || c == "enable" || c == "background" || c == "run ended" {
			t.Errorf("the scan was interfered with: %q", calls)
		}
	}
	if m.running {
		t.Error("the bypass started here during the scan")
	}
}

// C-4 contract: "restarting" (the engine restarts after a crash, the run
// still held) is the bypass on: stop and restart, no start, and the pill and
// Status say it restarts.
func TestRestartingStateIsOn(t *testing.T) {
	f := newFake()
	f.cfg.Strategy = &config.Strategy{ID: "tt-multidisorder"}
	f.st = supervisor.Status{State: "restarting", Mode: "service", PID: 77}
	m := newModel(t, f)
	if !m.bypassOn() || m.canStart() {
		t.Fatalf("bypassOn %v, canStart %v; want on", m.bypassOn(), m.canStart())
	}
	keys := actionKeys(statusPage{}.Actions(m))
	if !slices.Contains(keys, "s") || slices.Contains(keys, "r") {
		t.Errorf("Status keys %q, want stop and no start", keys)
	}
	if a, ok := m.pillAction(); !ok || a.Key != "s" {
		t.Errorf("the pill does %q, want stop", a.Key)
	}
	pill := ansi.Strip(m.pill(true))
	if pill != unicodeGlyphs.dot+" "+i18n.T("fix_arayuz.pill_restarting") {
		t.Errorf("pill %q", pill)
	}
	v := flat(ansi.Strip(statusPage{}.View(m, 76, 20)))
	has(t, v, i18n.T("fix_arayuz.pill_restarting"))
	has(t, v, i18n.T("fix_arayuz.how_restarting"))
	if strings.Contains(v, i18n.T("shell.pill.stopped")) || strings.Contains(v, i18n.T("shell.pill.running")) {
		t.Errorf("Status shows running or stopped:\n%s", v)
	}
	m.pending = true
	if _, ok := m.restartAction(); !ok {
		t.Error("R not offered for a restarting bypass with changes waiting")
	}
}

// A-8: a scan that stopped the service and started it again with every
// saved change leaves no restart pending, however fast it was.
func TestFastScanClearsPending(t *testing.T) {
	f := newFake()
	runningService(f)
	f.scanFn = func(context.Context, scan.Options, func(string)) (scan.Report, error) {
		return scan.Report{Winner: &config.Strategy{ID: "tt-multidisorder"}}, nil
	}
	m, cmd := toScan(t, f)
	m.pending = true
	if _, ok := m.restartAction(); ok {
		t.Error("R offered during the scan")
	}
	m = settle(t, m, cmd)
	if m.wiz.step != wDone {
		t.Fatalf("step %v, err %v", m.wiz.step, m.wiz.err)
	}
	if m.pending {
		t.Error("a restart is still pending after the scan restarted the service")
	}
	if bar, _ := m.hintBar(); strings.Contains(ansi.Strip(bar), i18n.T("tui.act.restart")) {
		t.Errorf("done offers R: %q", ansi.Strip(bar))
	}
}

// A-10: a restart under way holds R, so a second press stops nothing.
func TestRestartHeldUntilRunning(t *testing.T) {
	f := inBackgroundMode(newFake())
	f.st = supervisor.Status{State: "running", Mode: "background", PID: 4343}
	m := newModel(t, f)
	m.pending = true
	m, first := update(t, m, key(restartKey))
	m, second := update(t, m, key(restartKey))
	if first == nil || second != nil {
		t.Fatalf("R twice gave commands %v and %v; want one", first != nil, second != nil)
	}
	if bar, _ := m.hintBar(); strings.Contains(ansi.Strip(bar), i18n.T("tui.act.restart")) {
		t.Errorf("R still offered during the restart: %q", ansi.Strip(bar))
	}
	m = settle(t, m, first)
	if n := f.count("stop"); n != 1 || !f.called("background") {
		t.Errorf("calls %q, want one stop and the background start", f.calls)
	}
	if m.restarting {
		t.Error("still restarting once the bypass runs again")
	}
}

// A-10: a run in another terminal's foreground is restarted there: R does
// not kill it and pull it into this window, and Status and the note say so.
func TestRestartLeavesOtherTerminalRun(t *testing.T) {
	f := newFake()
	f.cfg.Strategy = &config.Strategy{ID: "tt-multidisorder"}
	f.st = supervisor.Status{State: "running", Mode: "foreground", PID: 4242}
	m := newModel(t, f)
	if got := m.changed(); got != i18n.T("fix_arayuz.restart_foreground") {
		t.Errorf("changed says %q", got)
	}
	if _, ok := m.restartAction(); ok {
		t.Error("R offered for another terminal's run")
	}
	if _, cmd := update(t, m, key(restartKey)); cmd != nil || f.called("stop") {
		t.Errorf("R acted on another terminal's run: %q", f.calls)
	}
	has(t, flat(ansi.Strip(statusPage{}.View(m, 76, 20))), i18n.T("fix_arayuz.pending_foreground"))
}
