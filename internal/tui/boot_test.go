package tui

import (
	"bytes"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/KadirBerkpolat1/SplitDPI/internal/config"
	"github.com/KadirBerkpolat1/SplitDPI/internal/i18n"
	"github.com/KadirBerkpolat1/SplitDPI/internal/ops"
	"github.com/KadirBerkpolat1/SplitDPI/internal/supervisor"
)

// chooseRunMode picks mode i of the run mode picker, from window.
func chooseRunMode(t *testing.T, f *fake, i int) Model {
	t.Helper()
	m := press(t, toSetting(t, f, "run_mode"), "enter")
	for range i {
		m = press(t, m, "down")
	}
	return press(t, m, "enter")
}

// inOrder fails unless the fake saw steps, in this order.
func inOrder(t *testing.T, f *fake, steps ...string) {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	at := -1
	for _, s := range steps {
		i := slices.Index(f.calls[at+1:], s)
		if i < 0 {
			t.Errorf("calls %q: want %q in this order", f.calls, steps)
			return
		}
		at += i + 1
	}
}

// The bug of Nov 7: with a background run on, Also at boot only saved the
// choice. Now it moves the run to the service at once, after asking. The
// TUI does not stop the run itself: Enable does, once its checks pass and
// the service is installed.
func TestBootChosenWhileBackgroundRuns(t *testing.T) {
	f := newFake()
	f.st = supervisor.Status{State: "running", Mode: "background", PID: 4343}
	m := chooseRunMode(t, f, 2)
	if m.dialog == nil || f.called("stop") || f.called("enable") || f.called("run mode boot") {
		t.Fatalf("Also at boot did not ask first: dialog %v, calls %q", m.dialog, f.calls)
	}
	v := flat(pageText(m.View()))
	has(t, v, "Start SplitDPI at boot?")
	has(t, v, "Stop the background run (pid 4343)")
	m = press(t, m, "y")
	inOrder(t, f, "enable", "service installed", "stop background")
	if f.called("stop") {
		t.Errorf("calls %q: the TUI stopped the run before Enable checked anything", f.calls)
	}
	if f.cfg.RunMode != config.RunBoot || m.cfg.RunMode != config.RunBoot {
		t.Errorf("run mode saved %q, shown %q; want boot", f.cfg.RunMode, m.cfg.RunMode)
	}
	has(t, flat(m.View()), i18n.T("service.enabled"))
	has(t, flat(fullList(m)), "Starts at boot yes")
	has(t, flat(statusPage{}.View(m, 60, 18)), "At boot yes")
}

// A failed enable keeps the run mode it had, and says why.
func TestBootChosenEnableFails(t *testing.T) {
	f := newFake()
	f.cfg.RunMode = config.RunBackground
	f.enableErr = ops.TakeoverError{Err: ops.ErrServiceNotStarted} // no run was there to stop
	m := press(t, chooseRunMode(t, f, 1), "y")
	if !f.called("enable") || f.cfg.RunMode != config.RunBackground || m.cfg.RunMode != config.RunBackground {
		t.Fatalf("calls %q, run mode saved %q, shown %q; want background kept", f.calls, f.cfg.RunMode, m.cfg.RunMode)
	}
	has(t, flat(m.View()), i18n.T("service.not_started"))
}

// A run told to stop that is still on its way down runs no bypass: the note
// says so and, whole on an 80x24 screen, how to start it once it has
// stopped, not that it still runs.
func TestBootTakeoverRunStillStopping(t *testing.T) {
	f := newFake()
	f.st = supervisor.Status{State: "running", Mode: "background", PID: 4343}
	// Enable's error once the run it stopped held the lock past both waits.
	f.enableErr = ops.TakeoverError{
		Stopped: "background", PID: 4343, Stopping: "background",
		Err: ops.StopRunError{Mode: "background", PID: 4343, Err: ops.ErrStopTimeout},
	}
	m := press(t, chooseRunMode(t, f, 2), "y")
	v := flat(m.View())
	has(t, v, "The background run (pid 4343) did not stop within 10 s, so the service was not started. "+
		"The background run is still stopping. Once it has stopped, start the bypass with: sudo splitdpi run --background")
	if strings.Contains(v, "still runs") {
		t.Errorf("says the bypass still runs:\n%s", m.View())
	}
}

// chooseBootHere picks Also at boot while the bypass runs in this window.
func chooseBootHere(t *testing.T, m Model) Model {
	t.Helper()
	m = press(t, toRow(t, goTo(t, m, pgSettings), "run_mode"), "enter")
	return press(t, press(t, press(t, m, "down"), "down"), "enter")
}

// The bypass this window runs stops, once the service is set up, and the
// service takes its place.
func TestBootChosenWhileRunningHere(t *testing.T) {
	f := newFake()
	m, done := runHere(t, f)
	m = chooseBootHere(t, m)
	has(t, flat(pageText(m.View())), "Stop the bypass running in this window")
	m = press(t, m, "y")
	<-done
	inOrder(t, f, "enable", "service installed", "run ended")
	if f.called("stop") {
		t.Errorf("calls %q", f.calls)
	}
	if f.cfg.RunMode != config.RunBoot || m.cfg.RunMode != config.RunBoot {
		t.Errorf("run mode saved %q, shown %q; want boot", f.cfg.RunMode, m.cfg.RunMode)
	}
	has(t, flat(m.View()), i18n.T("service.enabled"))
}

// The bug of the first round: the run here stopped before Enable checked
// anything. A failed check now leaves it running, and the run mode as it was.
func TestBootCheckFailsKeepsRunHere(t *testing.T) {
	f := newFake()
	f.enableErr = ops.UnsafeBinaryError{Path: "/opt/splitdpi/bin/splitdpi", At: "/opt/splitdpi", Reason: ops.UnsafeOwner, Install: "/usr/local/bin/splitdpi"}
	m, done := runHere(t, f)
	m = press(t, chooseBootHere(t, m), "y")
	if f.runCtx.Err() != nil || !m.running {
		t.Fatalf("the run here stopped: %v, running %v", f.runCtx.Err(), m.running)
	}
	if f.cfg.RunMode != config.RunWindow || m.cfg.RunMode != config.RunWindow {
		t.Errorf("run mode saved %q, shown %q; want window", f.cfg.RunMode, m.cfg.RunMode)
	}
	has(t, flat(m.View()), "The service cannot run /opt/splitdpi/bin/splitdpi: /opt/splitdpi does not belong to root.")
	m.stopRun()
	<-done
}

// A service that does not start once the run here stopped: the run starts
// again in the background, the run mode stays, and the note says so; or,
// when that fails too, that no bypass runs.
func TestBootStartFails(t *testing.T) {
	for _, tc := range []struct {
		name       string
		restartErr error
		want       string
	}{
		{"restarted", nil, "Could not start the service: systemctl: exit status 1. The previous run was restarted in the background (pid 4343)."},
		{"bypass down", ops.ErrEngineMissing, "Could not start the service: systemctl: exit status 1. The bypass is not running now. Start it with: sudo splitdpi run --background"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFake()
			f.startErr, f.restartErr = errors.New("systemctl: exit status 1"), tc.restartErr
			m, done := runHere(t, f)
			m = press(t, chooseBootHere(t, m), "y")
			<-done
			m, _ = update(t, m, runDoneMsg{})
			inOrder(t, f, "enable", "service installed", "run ended")
			if f.cfg.RunMode != config.RunWindow || m.cfg.RunMode != config.RunWindow {
				t.Errorf("run mode saved %q, shown %q; want window", f.cfg.RunMode, m.cfg.RunMode)
			}
			has(t, flat(m.View()), tc.want)
			if strings.Contains(m.View(), i18n.T("service.enabled")) {
				t.Errorf("says enabled:\n%s", m.View())
			}
		})
	}
}

// Leaving boot removes the service after asking, then saves the new mode.
func TestLeavingBootDisables(t *testing.T) {
	f := newFake()
	f.cfg.RunMode = config.RunBoot
	f.svc = ops.ServiceState{Supported: true, Installed: true, Enabled: true}
	f.st = supervisor.Status{State: "running", Mode: "service", PID: 77}
	m := press(t, press(t, press(t, toSetting(t, f, "run_mode"), "enter"), "up"), "enter")
	if m.dialog == nil || f.called("disable") {
		t.Fatalf("leaving boot did not ask first: dialog %v, calls %q", m.dialog, f.calls)
	}
	has(t, flat(pageText(m.View())), "Stop starting SplitDPI at boot?")
	m = press(t, m, "y")
	if i, j := slices.Index(f.calls, "disable"), slices.Index(f.calls, "run mode background"); i < 0 || j < i {
		t.Fatalf("calls %q: want the service removed, then background saved", f.calls)
	}
	if m.cfg.RunMode != config.RunBackground || m.svc.Enabled {
		t.Errorf("run mode %q, service %+v", m.cfg.RunMode, m.svc)
	}
	has(t, flat(m.View()), i18n.T("run_mode.disabled", i18n.T("run_mode.background")))

	// Between window and background nothing is asked or removed.
	f.calls = nil
	m = press(t, press(t, press(t, m, "enter"), "up"), "enter")
	if m.dialog != nil || f.called("disable") || !f.called("run mode window") {
		t.Errorf("dialog %v, calls %q", m.dialog, f.calls)
	}
}

// Run mode boot without the service, as the bug left it: Status and
// Settings warn, and r, or e while a bypass runs, sets it up.
func TestBootModeWithoutService(t *testing.T) {
	f := newFake()
	f.cfg.RunMode = config.RunBoot
	m := newModel(t, f)
	has(t, flat(statusPage{}.View(m, 60, 18)), "At boot no, the service is not set up")
	if m.indexOf("e") >= 0 || m.indexOf("r") < 0 {
		t.Errorf("stopped: actions %q, want r and no e", reproKeys(m))
	}
	if m = press(t, m, "r"); m.dialog == nil {
		t.Fatal("r did not offer to set the service up")
	}
	m = press(t, m, "n")

	f.st = supervisor.Status{State: "running", Mode: "background", PID: 4343}
	m = newModel(t, f)
	if m.indexOf("e") < 0 {
		t.Fatalf("running: actions %q, want e", reproKeys(m))
	}
	m = press(t, press(t, m, "e"), "y")
	inOrder(t, f, "enable", "service installed", "stop background")

	f = newFake()
	f.cfg.RunMode = config.RunBoot
	m = toSetting(t, f, "boot")
	has(t, flat(fullList(m)), "Starts at boot no, the service is not set up")
	if cli := footerCLI(m); cli != "sudo splitdpi enable" {
		t.Errorf("boot row CLI %q", cli)
	}
	if m = press(t, press(t, m, "enter"), "y"); !f.called("enable") {
		t.Errorf("enter on the boot row: calls %q", f.calls)
	}
}

// Quitting a run here in boot mode moves it to the service, not to the
// background: the run stops only once the service is set up, and the TUI
// quits once the service runs and the run here has ended, in whichever
// order it hears of the two.
func TestQuitToServiceInBootMode(t *testing.T) {
	for _, tc := range []struct {
		name         string
		serviceFirst bool
	}{{"run ends first", false}, {"service done first", true}} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFake()
			m, done := runHere(t, f)
			f.cfg.RunMode, m.cfg.RunMode = config.RunBoot, config.RunBoot
			m, _ = update(t, m, key("q"))
			has(t, m.View(), "b "+i18n.T("background_tui.quit_boot"))
			m, cmd := update(t, m, key("b"))
			if f.runCtx.Err() != nil {
				t.Fatal("the run here stopped before Enable checked anything")
			}
			serviceDone := cmd()
			<-done
			first, second := tea.Msg(runDoneMsg{}), serviceDone
			if tc.serviceFirst {
				first, second = second, first
			}
			m, cmd = update(t, m, first)
			if cmd != nil {
				if _, ok := cmd().(tea.QuitMsg); ok {
					t.Fatal("quit before the service ran and the run here ended")
				}
			}
			if m.confirmQuit {
				t.Fatal("asks about a run here that has ended")
			}
			m, cmd = update(t, m, second)
			if cmd == nil {
				t.Fatal("did not quit once the service ran")
			}
			if _, ok := cmd().(tea.QuitMsg); !ok {
				t.Fatal("did not quit once the service ran")
			}
			inOrder(t, f, "enable", "service installed", "run ended")
			if f.called("background") {
				t.Errorf("calls %q", f.calls)
			}
			var out bytes.Buffer
			m.printQuitNote(&out)
			has(t, out.String(), i18n.T("service.enabled"))
		})
	}
}

// Quitting while the service takes over waits until it is done: Bubbletea
// does not wait for a running command, and a takeover cut short could leave
// no bypass running. Then it quits and says what the takeover did; a failed
// takeover stays on screen.
func TestQuitWaitsForTakeover(t *testing.T) {
	for _, tc := range []struct {
		name     string
		startErr error
	}{{"service runs", nil}, {"service fails", errors.New("systemctl: exit status 1")}} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFake()
			f.cfg.RunMode = config.RunBoot
			f.st = supervisor.Status{State: "running", Mode: "background", PID: 4343}
			f.startErr, f.gate = tc.startErr, make(chan struct{})
			m := press(t, newModel(t, f), "e")
			m, cmd := update(t, m, key("y"))
			msgs := make(chan tea.Msg, 1)
			go func() { msgs <- cmd() }()

			if m = press(t, m, "e"); m.dialog != nil {
				t.Error("a second takeover offered while one is under way")
			}
			has(t, flat(m.View()), flat(i18n.T("tui.service_busy")))
			for _, k := range []tea.KeyMsg{key("q"), {Type: tea.KeyCtrlC}} {
				var c tea.Cmd
				if m, c = update(t, m, k); c != nil {
					t.Fatalf("%s during the takeover returned a command", k)
				}
			}
			if m.confirmQuit {
				t.Error("quit prompt shown")
			}
			has(t, flat(m.View()), flat(i18n.T("tui.service_wait")))

			close(f.gate)
			m, c := update(t, m, <-msgs)
			_, quit := c().(tea.QuitMsg)
			if quit != (tc.startErr == nil) {
				t.Errorf("quit %v once the takeover ended with %v", quit, tc.startErr)
			}
			var out bytes.Buffer
			m.printQuitNote(&out)
			if tc.startErr != nil {
				has(t, flat(m.View()), "The previous run was restarted in the background (pid 4343).")
				if out.Len() > 0 {
					t.Errorf("printed %q without quitting", out.String())
				}
				return
			}
			has(t, out.String(), i18n.T("service.enabled"))
		})
	}
}

// A quit that waited for the service change asks first when this window
// still runs the bypass: quitting would stop it.
func TestQuitAfterServiceAsksForRunHere(t *testing.T) {
	f := newFake()
	f.svc = ops.ServiceState{Supported: true, Installed: true, Enabled: true}
	m, done := runHere(t, f)
	f.gate = make(chan struct{})
	m = press(t, toRow(t, goTo(t, m, pgSettings), "run_mode"), "enter")
	m = press(t, press(t, m, "down"), "enter") // background: the service goes
	m, cmd := update(t, m, key("y"))
	msgs := make(chan tea.Msg, 1)
	go func(disable tea.Cmd) { msgs <- disable() }(cmd)
	if m, cmd = update(t, m, key("q")); cmd != nil || m.confirmQuit {
		t.Fatalf("q during the change: prompt %v, command %v", m.confirmQuit, cmd != nil)
	}

	close(f.gate)
	m, cmd = update(t, m, <-msgs)
	if cmd != nil {
		if _, ok := cmd().(tea.QuitMsg); ok {
			t.Fatal("quit with the bypass running in this window")
		}
	}
	if !m.confirmQuit || f.runCtx.Err() != nil {
		t.Errorf("prompt %v, run here ended %v", m.confirmQuit, f.runCtx.Err())
	}
	if !f.called("disable") || f.cfg.RunMode != config.RunBackground {
		t.Errorf("calls %q, run mode %q", f.calls, f.cfg.RunMode)
	}
	m.stopRun()
	<-done
}

// Run waits for a service change the program ended in the middle of, and
// says how it ended.
func TestWaitService(t *testing.T) {
	m := newModel(t, newFake())
	op := &serviceOp{done: make(chan struct{})}
	m.svcOp = op
	var out bytes.Buffer
	returned := make(chan struct{})
	go func() { m.waitService(&out); close(returned) }()
	select {
	case <-returned:
		t.Fatal("returned before the change was done")
	case <-time.After(50 * time.Millisecond):
	}
	op.err = ops.TakeoverError{Stopped: "background", PID: 4343, Err: ops.ErrServiceNotStarted, Restarted: 4545}
	op.msg = enableMsg{op.err}
	close(op.done)
	<-returned
	has(t, out.String(), i18n.T("tui.service_wait"))
	has(t, out.String(), "The previous run was restarted in the background (pid 4545).")
}

// Where the init has no service, boot is not offered and the picker says why.
func TestBootHiddenWithoutService(t *testing.T) {
	f := newFake()
	f.svc = ops.ServiceState{}
	m := press(t, toSetting(t, f, "run_mode"), "enter")
	v := flat(pageText(m.View()))
	has(t, v, flat(i18n.T("service.unsupported")))
	if strings.Contains(v, i18n.T("run_mode.boot")) {
		t.Errorf("boot offered:\n%s", v)
	}
	if m = press(t, press(t, m, "down"), "down"); m.settings.pick.cursor != 0 {
		t.Errorf("cursor %d, want two choices", m.settings.pick.cursor)
	}
	has(t, flat(fullList(press(t, m, "esc"))), "Starts at boot no, not supported here")
}

func reproKeys(m Model) []string {
	var ks []string
	for _, a := range m.actions() {
		ks = append(ks, a.Key)
	}
	return ks
}
