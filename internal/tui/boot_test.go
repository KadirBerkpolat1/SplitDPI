package tui

import (
	"bytes"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/KadirBerkpolat1/SplitDPI/internal/config"
	"github.com/KadirBerkpolat1/SplitDPI/internal/i18n"
	"github.com/KadirBerkpolat1/SplitDPI/internal/ops"
	"github.com/KadirBerkpolat1/SplitDPI/internal/supervisor"
)

// pickRunMode chooses mode in the open run mode picker.
func pickRunMode(t *testing.T, m Model, mode config.RunMode) Model {
	t.Helper()
	for range runModes {
		if m.runModeChoices()[m.settings.pick.cursor] == mode {
			return press(t, m, "enter")
		}
		m = press(t, m, "down")
	}
	t.Fatalf("no run mode %s in the picker", mode)
	return m
}

// chooseRunMode chooses mode on the settings page.
func chooseRunMode(t *testing.T, f *fake, mode config.RunMode) Model {
	t.Helper()
	return pickRunMode(t, press(t, toSetting(t, f, "run_mode"), "enter"), mode)
}

// popupText is the popup the screen shows, its lines as the screen has them.
func popupText(m Model) string {
	p, ok := m.popupBox()
	if !ok {
		return ""
	}
	return ansi.Strip(strings.Join(m.fit(p, m.popupRoom(), m.popupOffset()).lines, "\n"))
}

// popupWhole fails unless the popup shows down to its keys and its bottom border.
func popupWhole(t *testing.T, name string, m Model) {
	t.Helper()
	spans, _, ok := m.popupSpans()
	lines := strings.Split(m.View(), "\n")
	if !ok || len(spans) == 0 {
		t.Errorf("%s: no popup keys:\n%s", name, m.View())
		return
	}
	if y := spans[len(spans)-1].y; y+1 >= len(lines)-1 || !strings.Contains(lines[y+1], "╰") {
		t.Errorf("%s: popup cut off:\n%s", name, m.View())
	}
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
	m := chooseRunMode(t, f, config.RunBoot)
	if m.dialog == nil || f.called("stop") || f.called("enable") || f.called("run mode boot") {
		t.Fatalf("Also at boot did not ask first: dialog %v, calls %q", m.dialog, f.calls)
	}
	v := flat(popupText(m))
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
	has(t, flat(fullList(m)), "Start at boot Yes, at every boot")
	has(t, flat(statusPage{}.View(m, 60, 18)), "At boot yes")
}

// A failed enable keeps the run mode it had, and says why.
func TestBootChosenEnableFails(t *testing.T) {
	f := newFake()
	f.cfg.RunMode = config.RunBackground
	f.enableErr = ops.TakeoverError{Err: ops.ErrServiceNotStarted} // no run was there to stop
	m := press(t, chooseRunMode(t, f, config.RunBoot), "y")
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
	m := press(t, chooseRunMode(t, f, config.RunBoot), "y")
	v := flat(m.View())
	has(t, v, "The background run is still stopping. Once it has stopped, start the bypass with: sudo splitdpi run --background")
	if strings.Contains(v, "still runs") {
		t.Errorf("says the bypass still runs:\n%s", m.View())
	}
}

// The command that starts the bypass again shows whole on one line of the
// note at 80x24, in every language and with a 7-digit pid: lipgloss breaks
// lines at a hyphen, and a split command cannot be copied. What runs now
// and what to do stay; why the service did not start gives way first.
func TestTakeoverNoteKeepsCommandWhole(t *testing.T) {
	defer i18n.Init("en")
	const pid, cmd = 1234567, "sudo splitdpi run --background"
	stop := ops.StopRunError{Mode: "background", PID: pid, Err: ops.ErrStopTimeout}
	for _, lang := range []string{"en", "tr", "ru"} {
		i18n.Init(lang)
		for name, e := range map[string]ops.TakeoverError{
			"stop slow, bypass down":       {Stopped: "background", PID: pid, Err: stop, RestartErr: ops.ErrEngineMissing},
			"start failed, still stopping": {Stopped: "background", PID: pid, Err: errors.New("systemctl: exit status 1"), Stopping: "background"},
		} {
			f := newFake()
			f.st = supervisor.Status{State: "running", Mode: "background", PID: pid}
			f.enableErr = e
			v := press(t, chooseRunMode(t, f, config.RunBoot), "y").View()
			fits(t, lang+" "+name, v)
			whole := slices.ContainsFunc(strings.Split(v, "\n"), func(l string) bool {
				return !strings.HasPrefix(l, "$ ") && strings.Contains(l, cmd)
			})
			if !whole {
				t.Errorf("%s %s: %q is not whole on one line:\n%s", lang, name, cmd, v)
			}
			lead := i18n.T("service.takeover_down")
			if e.Stopping != "" {
				lead = i18n.T("service.takeover_stopping", i18n.Enum("mode", e.Stopping))
			}
			has(t, flat(v), flat(lead))
		}
	}
}

// An error that ends with a period, as systemctl's stderr does, ends its
// line of the note with one period, not two.
func TestTakeoverNoteOnePeriod(t *testing.T) {
	err := errors.New("systemctl: exit status 1: Job for splitdpi.service failed because the control process exited with error code.\n" +
		`See "systemctl status splitdpi.service" and "journalctl -xeu splitdpi.service" for details.`)
	for name, e := range map[string]ops.TakeoverError{
		"start": {Err: err},
		"undo":  {Err: ops.ErrServiceNotStarted, Undo: err},
		"stop":  {Stopped: "background", PID: 4343, Err: ops.StopRunError{Mode: "background", PID: 4343, Err: err}, Running: "background"},
	} {
		if note := takeoverText(e); strings.Contains(note, "..") || !strings.Contains(note, "error code.") {
			t.Errorf("%s: %q", name, note)
		}
	}
}

// The enable confirm says how long the bypass is off and what happens if
// the service does not start, whole on an 80x24 screen down to its keys,
// in every language, with a 7-digit pid or with the run in this window.
func TestEnableConfirmFits(t *testing.T) {
	defer i18n.Init("en")
	for _, lang := range []string{"en", "tr", "ru"} {
		i18n.Init(lang)
		f := newFake()
		f.st = supervisor.Status{State: "running", Mode: "background", PID: 1234567}
		m := chooseRunMode(t, f, config.RunBoot)
		here, done := runHere(t, newFake())
		here = chooseBootHere(t, here)
		for name, c := range map[string]struct {
			m    Model
			move string
		}{
			"other run": {m, i18n.T("shell.enable.move", i18n.Enum("mode", "background"), 1234567)},
			"run here":  {here, i18n.T("shell.enable.move_here")},
		} {
			fits(t, lang+" "+name, c.m.View())
			popupWhole(t, lang+" "+name, c.m)
			if !strings.Contains(flat(popupText(c.m)), flat(c.move)) {
				t.Errorf("%s %s: confirm without %q:\n%s", lang, name, c.move, c.m.View())
			}
		}
		here.stopRun()
		<-done
	}
}

// chooseBootHere picks start at boot while the bypass runs in this window.
func chooseBootHere(t *testing.T, m Model) Model {
	t.Helper()
	return pickRunMode(t, press(t, toRow(t, goTo(t, m, pgSettings), "run_mode"), "enter"), config.RunBoot)
}

// The bypass this window runs stops, once the service is set up, and the
// service takes its place.
func TestBootChosenWhileRunningHere(t *testing.T) {
	f := newFake()
	m, done := runHere(t, f)
	m = chooseBootHere(t, m)
	has(t, flat(popupText(m)), "Stop the bypass running in this window")
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
	m := pickRunMode(t, press(t, toSetting(t, f, "run_mode"), "enter"), config.RunBackground)
	if m.dialog == nil || f.called("disable") {
		t.Fatalf("leaving boot did not ask first: dialog %v, calls %q", m.dialog, f.calls)
	}
	has(t, flat(popupText(m)), "Stop starting SplitDPI at boot?")
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
	m = pickRunMode(t, press(t, m, "enter"), config.RunWindow)
	if m.dialog != nil || f.called("disable") || !f.called("run mode window") {
		t.Errorf("dialog %v, calls %q", m.dialog, f.calls)
	}
}

// Run mode boot without the service, as the bug left it: Status and
// Settings warn, and r, or b while a bypass runs, sets it up.
func TestBootModeWithoutService(t *testing.T) {
	f := newFake()
	f.cfg.RunMode = config.RunBoot
	f.cfg.Strategy = &config.Strategy{ID: "tt-multidisorder"}
	m := newModel(t, f)
	has(t, flat(statusPage{}.View(m, 60, 18)), "At boot no, the service is not set up")
	if cliFor(m, "r") != "sudo splitdpi enable" {
		t.Errorf("stopped: actions %q, want r to set it up", reproKeys(m))
	}
	if m = press(t, m, "r"); m.dialog == nil {
		t.Fatal("r did not offer to set the service up")
	}
	m = press(t, m, "n")

	f.st = supervisor.Status{State: "running", Mode: "background", PID: 4343}
	m = newModel(t, f)
	if cliFor(m, "b") != "sudo splitdpi enable" {
		t.Fatalf("running: actions %q, want b", reproKeys(m))
	}
	m = press(t, press(t, m, "b"), "y")
	inOrder(t, f, "enable", "service installed", "stop background")

	f = newFake()
	f.cfg.RunMode = config.RunBoot
	m = toSetting(t, f, "run_mode")
	has(t, flat(fullList(m)), "Start at boot no, the service is not set up")
	if cli := footerCLI(m); cli != "sudo splitdpi enable" {
		t.Errorf("run mode row CLI %q", cli)
	}
	if m = press(t, press(t, m, "enter"), "y"); !f.called("enable") {
		t.Errorf("enter on the run mode row: calls %q", f.calls)
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
			f.cfg.Strategy = &config.Strategy{ID: "tt-multidisorder"}
			f.st = supervisor.Status{State: "running", Mode: "background", PID: 4343}
			f.startErr, f.gate = tc.startErr, make(chan struct{})
			m := press(t, newModel(t, f), "b")
			m, cmd := update(t, m, key("y"))
			msgs := make(chan tea.Msg, 1)
			go func() { msgs <- cmd() }()

			if m = press(t, m, "b"); m.dialog != nil {
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
	m = pickRunMode(t, m, config.RunBackground) // the service goes
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
	has(t, v, flat(i18n.T("tui.boot_unsupported")))
	if strings.Contains(v, i18n.T("run_mode.boot")) || strings.Contains(v, "splitdpi run") {
		t.Errorf("boot or a command offered:\n%s", v)
	}
	start := m.settings.pick.cursor
	if m = press(t, press(t, m, "down"), "down"); m.settings.pick.cursor != start {
		t.Errorf("cursor %d, want two choices", m.settings.pick.cursor)
	}
	has(t, flat(fullList(press(t, m, "esc"))), "Start at boot No, in this window")
}

func reproKeys(m Model) []string {
	var ks []string
	for _, a := range m.actions() {
		ks = append(ks, a.Key)
	}
	return ks
}
