package tui

import (
	"context"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/KadirBerkpolat1/SplitDPI/internal/config"
	"github.com/KadirBerkpolat1/SplitDPI/internal/i18n"
	"github.com/KadirBerkpolat1/SplitDPI/internal/ops"
	"github.com/KadirBerkpolat1/SplitDPI/internal/scan"
	"github.com/KadirBerkpolat1/SplitDPI/internal/supervisor"
)

func pillText(m Model) string { return ansi.Strip(m.pill(true)) }

// U-14: while a setup scan holds the service it stopped, Settings changes
// neither the service nor the bypass: the scan starts the service again as
// it ran, which would undo them.
func TestScanHoldsServiceChanges(t *testing.T) {
	f := newFake()
	f.plan = ops.UninstallPlan{Service: "splitdpi.service"}
	f.takenOver = []string{"zapret2"}
	m := scanningUnder(t, f, context.Background())
	defer func() { m.wiz.scanCancel(); m.wiz.done.wait() }()
	m.foreign = []ops.Foreign{zapret2()}
	want := i18n.T("fix2_arayuz.scan_wait")

	m = press(t, toRow(t, goTo(t, m, pgSettings), "run_mode"), "enter")
	m = pickRunMode(t, m, config.RunBackground)
	if m.dialog != nil || m.note != want {
		t.Errorf("run mode background during the scan: dialog %v, note %q", m.dialog != nil, m.note)
	}
	for name, do := range map[string]func(*Model) tea.Cmd{
		"enable":    (*Model).confirmEnable,
		"disable":   func(m *Model) tea.Cmd { return m.confirmDisable(config.RunWindow) },
		"takeover":  (*Model).confirmTakeover,
		"revert":    (*Model).confirmRevert,
		"uninstall": (*Model).confirmUninstall,
	} {
		mm := m
		mm.note = ""
		if cmd := do(&mm); cmd != nil || mm.dialog != nil || mm.note != want {
			t.Errorf("%s during the scan: command %v, dialog %v, note %q", name, cmd != nil, mm.dialog != nil, mm.note)
		}
	}
	if f.called("disable") || f.called("uninstall") || f.called("revert") {
		t.Errorf("the service changed during the scan: %q", f.calls)
	}
}

// U-10, U-15: a service restart holds until Enable returns: the pill and
// Status show the restart, not a stopped bypass to start, and r starts no
// second Enable beside the first.
func TestRestartHeldUntilServiceRuns(t *testing.T) {
	f := newFake()
	runningService(f)
	f.cfg.Strategy = &config.Strategy{ID: "tt-multidisorder"}
	f.cfg.RunMode = config.RunBoot
	m := newModel(t, f)
	m.pending = true
	m, start := update(t, m, key(restartKey))
	if m.svcOp == nil {
		t.Fatalf("the restart is no service change under way: %q", f.calls)
	}
	if p := pillText(m); p != unicodeGlyphs.dot+" "+i18n.T("fix_arayuz.pill_restarting") {
		t.Errorf("pill %q while Enable runs", p)
	}
	v := flat(ansi.Strip(statusPage{}.View(m, 76, 20)))
	if strings.Contains(v, i18n.T("shell.pill.stopped")) || !strings.Contains(v, i18n.T("fix_arayuz.pill_restarting")) {
		t.Errorf("Status while Enable runs:\n%s", v)
	}
	if m.canStart() || slices.Contains(actionKeys(statusPage{}.Actions(m)), "r") {
		t.Errorf("start offered while Enable runs: %q", actionKeys(statusPage{}.Actions(m)))
	}
	if _, cmd := update(t, m, key("r")); cmd != nil {
		t.Error("r while Enable runs gave a command")
	}
	if mm := m; mm.enable() != nil {
		t.Error("a second Enable started beside the first")
	}
	m = settle(t, m, start)
	if n := f.count("enable"); n != 1 || m.restarting {
		t.Errorf("enables %d, restarting %v; calls %q", n, m.restarting, f.calls)
	}
	if p := pillText(m); p != unicodeGlyphs.dot+" "+i18n.T("shell.pill.running") {
		t.Errorf("pill %q once the service runs", p)
	}
}

// U-15: a scan waits for a restart under way, which would start the bypass
// in the middle of it, and runs once the restart has ended.
func TestScanWaitsForRestart(t *testing.T) {
	f := newFake()
	f.st = supervisor.Status{State: "running", Mode: "background", PID: 4343}
	f.cfg.RunMode = config.RunBackground
	f.set = trSet()
	f.detected = scan.ASNInfo{ASN: 9121, Country: "TR", Org: "Turk Telekom"}
	f.cfg.Apps = nil
	f.scanFn = func(context.Context, scan.Options, func(string)) (scan.Report, error) {
		f.record("scan")
		return scan.Report{Winner: &config.Strategy{ID: "tt-multidisorder"}}, nil
	}
	m := newModel(t, f)
	m.pending = true
	m, stop := update(t, m, key(restartKey))
	m = press(t, m, "w")
	if m.wiz.step != wApps {
		t.Fatalf("step %v, want the apps", m.wiz.step)
	}
	m, cmd := update(t, m, key("enter"))
	if cmd != nil || m.wiz.scanning || m.note != i18n.T("fix2_arayuz.restart_wait") {
		t.Errorf("scan during the restart: command %v, scanning %v, note %q", cmd != nil, m.wiz.scanning, m.note)
	}
	m = settle(t, m, stop)
	if m.restarting || !f.called("background") {
		t.Fatalf("restart did not end: restarting %v, calls %q", m.restarting, f.calls)
	}
	m, cmd = update(t, m, key("enter"))
	if cmd == nil || !m.wiz.scanning {
		t.Fatalf("no scan once the restart ended; note %q", m.note)
	}
	m = settle(t, m, cmd)
	if i, j := slices.Index(f.calls, "background"), slices.Index(f.calls, "scan"); j < 0 || i > j {
		t.Errorf("calls %q, want the restart's start before the scan", f.calls)
	}
}

// U-15: the stop of a restart that ends while a scan runs starts nothing.
func TestRestartStartsNothingDuringScan(t *testing.T) {
	f := inBackgroundMode(newFake())
	m := newModel(t, f)
	m.wiz.scanning, m.restarting = true, true
	m = settle(t, m, func() tea.Msg { return restartStoppedMsg{} })
	if f.called("background") || m.restarting {
		t.Errorf("calls %q, restarting %v", f.calls, m.restarting)
	}
	if m.note != i18n.T("fix2_arayuz.restart_scan") {
		t.Errorf("note %q", m.note)
	}
}

// U-10, U-15: the done step offers no restart of another terminal's run, nor
// while a restart is under way.
func TestDoneOffersNoRestartOfOtherTerminalOrHeld(t *testing.T) {
	for name, st := range map[string]supervisor.Status{
		"other terminal": {State: "running", Mode: "foreground", PID: 4242},
		"restarting":     {State: "running", Mode: "service", PID: 77},
	} {
		f := newFake()
		f.cfg.Strategy = &config.Strategy{ID: "tt-multidisorder"}
		f.st = st
		m := newModel(t, f)
		m.page, m.wiz.step, m.restarting = pgSetup, wDone, name == "restarting"
		if keys := actionKeys(m.doneActions()); slices.Contains(keys, "r") {
			t.Errorf("%s: done offers %q", name, keys)
		}
	}
}

// U-16: an engine that restarts after a crash shows as restarting in the
// pill and on Status also for this window's run.
func TestWindowRunShowsRestarting(t *testing.T) {
	f := newFake()
	m, done := runHere(t, f)
	defer func() { m.stopRun(); <-done }()
	f.mu.Lock()
	f.st = supervisor.Status{State: "restarting", Mode: "foreground", PID: os.Getpid()}
	f.mu.Unlock()
	m, _ = update(t, m, readStatus(f))
	if p := pillText(m); p != unicodeGlyphs.dot+" "+i18n.T("fix_arayuz.pill_restarting") {
		t.Errorf("pill %q", p)
	}
	v := flat(ansi.Strip(statusPage{}.View(m, 76, 20)))
	has(t, v, i18n.T("fix_arayuz.how_restarting"))
	if strings.Contains(v, i18n.T("shell.pill.running")) {
		t.Errorf("Status shows running:\n%s", v)
	}
}

// U-18: q waits for a background start under way, and quits once it runs.
func TestQuitWaitsForBackgroundStart(t *testing.T) {
	f := inBackgroundMode(newFake())
	f.bgGate = make(chan struct{})
	m, start := update(t, newModel(t, f), key("r"))
	msgs := make(chan tea.Msg, 1)
	go func() { msgs <- start() }()
	m, quit := update(t, m, key("q"))
	if quit != nil {
		t.Error("q quit before the background start ended")
	}
	close(f.bgGate)
	m, quit = update(t, m, <-msgs)
	if quit == nil {
		t.Fatal("no quit once the background start ended")
	}
	if _, ok := quit().(tea.QuitMsg); !ok || m.bgPID != 4343 {
		t.Errorf("quit %v, background pid %d", ok, m.bgPID)
	}
}

// U-18: a signal ends the program in the middle of a background start: the
// start goes on to its end, and Run waits for it.
func TestRunWaitsForBackgroundStart(t *testing.T) {
	f := inBackgroundMode(newFake())
	f.bgGate = make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := newModel(t, f)
	m.ctx = ctx
	m, start := update(t, m, key("r"))
	go start()
	for !f.called("background") {
		time.Sleep(time.Millisecond)
	}
	before := f.count("detect foreign")
	err := runProgram(t, m, eioTerminal{}, func() {
		for f.count("detect foreign") == before { // Init ran: the loop is up
			time.Sleep(time.Millisecond)
		}
		cancel()
		time.AfterFunc(50*time.Millisecond, func() { close(f.bgGate) })
	})
	if err != nil {
		t.Errorf("Run: %v", err)
	}
	if !f.called("background done") || f.called("background cut") {
		t.Errorf("Run returned before the background start ended, or cut it: %q", f.calls)
	}
}

// U-15b: q right after R waits until the service or background run the
// restart stopped runs again, and quits only then.
func TestQuitWaitsForRestart(t *testing.T) {
	for _, mode := range []string{"service", "background"} {
		t.Run(mode, func(t *testing.T) {
			f := newFake()
			start := "background"
			if mode == "service" {
				runningService(f)
				f.cfg.Strategy = &config.Strategy{ID: "tt-multidisorder"}
				f.cfg.RunMode, start = config.RunBoot, "enable"
			} else {
				inBackgroundMode(f)
				f.st = supervisor.Status{State: "running", Mode: "background", PID: 4343}
			}
			m := newModel(t, f)
			m.pending = true
			m, restart := update(t, m, key(restartKey))
			if restart == nil {
				t.Fatal("R gave no command")
			}
			m, quit := update(t, m, key("q"))
			if quit != nil {
				t.Fatal("q quit before the restart started the bypass again")
			}
			msg := restart()
			if !f.called("stop") || !f.called(start) {
				t.Fatalf("calls %q, want stop and %s", f.calls, start)
			}
			m, quit = update(t, m, msg)
			if quit == nil {
				t.Fatal("no quit once the restart ended")
			}
			if _, ok := quit().(tea.QuitMsg); !ok {
				t.Error("the restart's end did not quit")
			}
			if f.st.State != "running" || f.st.Mode != mode {
				t.Errorf("left %s/%s, want %s running", f.st.State, f.st.Mode, mode)
			}
		})
	}
}

// U-15b: a signal in the middle of a service restart ends the program, and
// Run waits until the service runs again; Enable goes on to its end.
func TestRunWaitsForRestart(t *testing.T) {
	f := newFake()
	runningService(f)
	f.cfg.Strategy = &config.Strategy{ID: "tt-multidisorder"}
	f.cfg.RunMode = config.RunBoot
	f.gate = make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := newModel(t, f)
	m.ctx, m.pending = ctx, true
	m, restart := update(t, m, key(restartKey))
	go restart()
	for !f.called("stop") {
		time.Sleep(time.Millisecond)
	}
	before := f.count("detect foreign")
	err := runProgram(t, m, eioTerminal{}, func() {
		for f.count("detect foreign") == before { // Init ran: the loop is up
			time.Sleep(time.Millisecond)
		}
		cancel()
		time.AfterFunc(50*time.Millisecond, func() { close(f.gate) })
	})
	if err != nil {
		t.Errorf("Run: %v", err)
	}
	f.mu.Lock()
	st, svcCtx := f.st, f.svcCtx
	f.mu.Unlock()
	if st.State != "running" || st.Mode != "service" {
		t.Errorf("Run left the service %s/%s: %q", st.State, st.Mode, f.calls)
	}
	if svcCtx == nil || svcCtx.Err() != nil {
		t.Error("Enable ran with the program's ended context")
	}
}

// U-15b: Enable and Disable get a context that the program's end does not
// cancel: Run waits for them, and cut short they could leave no bypass.
func TestServiceChangeOutlivesProgramContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for name, change := range map[string]func(*Model) tea.Cmd{
		"enable":  (*Model).enable,
		"disable": func(m *Model) tea.Cmd { return m.disable(config.RunWindow) },
	} {
		f := newFake()
		f.cfg.Strategy = &config.Strategy{ID: "tt-multidisorder"}
		m := newModel(t, f)
		m.ctx = ctx
		change(&m)()
		f.mu.Lock()
		c := f.svcCtx
		f.mu.Unlock()
		if c == nil || c.Err() != nil {
			t.Errorf("%s ran with the program's cancelled context", name)
		}
	}
}
