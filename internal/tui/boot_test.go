package tui

import (
	"bytes"
	"slices"
	"strings"
	"testing"

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

// The bug of Nov 7: with a background run on, Also at boot only saved the
// choice. Now it moves the run to the service at once, after asking.
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
	if i, j := slices.Index(f.calls, "stop"), slices.Index(f.calls, "enable"); i < 0 || j < i {
		t.Fatalf("calls %q: want the background run stopped, then the service enabled", f.calls)
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
	f.enableErr = ops.ErrServiceNotStarted
	m := press(t, chooseRunMode(t, f, 1), "y")
	if !f.called("enable") || f.cfg.RunMode != config.RunBackground || m.cfg.RunMode != config.RunBackground {
		t.Fatalf("calls %q, run mode saved %q, shown %q; want background kept", f.calls, f.cfg.RunMode, m.cfg.RunMode)
	}
	has(t, flat(m.View()), i18n.T("service.not_started"))
}

// The bypass this window runs stops, and the service takes its place.
func TestBootChosenWhileRunningHere(t *testing.T) {
	f := newFake()
	m, done := runHere(t, f)
	m = press(t, toRow(t, goTo(t, m, pgSettings), "run_mode"), "enter")
	m = press(t, press(t, press(t, m, "down"), "down"), "enter")
	has(t, flat(pageText(m.View())), "Stop the bypass running in this window")
	m = press(t, m, "y")
	<-done
	if i, j := slices.Index(f.calls, "run ended"), slices.Index(f.calls, "enable"); i < 0 || j < i || f.called("stop") {
		t.Errorf("calls %q: want the run here ended, then the service enabled", f.calls)
	}
	if f.cfg.RunMode != config.RunBoot || m.cfg.RunMode != config.RunBoot {
		t.Errorf("run mode saved %q, shown %q; want boot", f.cfg.RunMode, m.cfg.RunMode)
	}
	has(t, flat(m.View()), i18n.T("service.enabled"))
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
	if i, j := slices.Index(f.calls, "stop"), slices.Index(f.calls, "enable"); i < 0 || j < i {
		t.Errorf("calls %q", f.calls)
	}

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
// background.
func TestQuitToServiceInBootMode(t *testing.T) {
	f := newFake()
	m, done := runHere(t, f)
	f.cfg.RunMode, m.cfg.RunMode = config.RunBoot, config.RunBoot
	m, _ = update(t, m, key("q"))
	has(t, m.View(), "b "+i18n.T("background_tui.quit_boot"))
	m, _ = update(t, m, key("b"))
	<-done
	m, cmd := update(t, m, runDoneMsg{})
	m, cmd = update(t, m, cmd())
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("did not quit once the service ran")
	}
	if i, j := slices.Index(f.calls, "run ended"), slices.Index(f.calls, "enable"); i < 0 || j < i || f.called("background") {
		t.Errorf("calls %q: want the run here ended, then the service enabled", f.calls)
	}
	var out bytes.Buffer
	m.printService(&out)
	has(t, out.String(), i18n.T("service.enabled"))
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
