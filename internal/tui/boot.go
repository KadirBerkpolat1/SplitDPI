package tui

import (
	"errors"
	"fmt"
	"io"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/KadirBerkpolat1/SplitDPI/internal/config"
	"github.com/KadirBerkpolat1/SplitDPI/internal/i18n"
	"github.com/KadirBerkpolat1/SplitDPI/internal/ops"
)

// Starting at boot. Only the init system says whether SplitDPI starts at
// boot; run mode boot asks for it. Choosing boot sets the service up at once,
// and a bypass that runs now moves to it; leaving boot removes the service.

type enableMsg struct{ err error }

// otherRun is whether another process runs the bypass in the background or
// in the foreground: enabling the service stops that run. The bypass this
// TUI runs is m.running.
func (m Model) otherRun() bool {
	return !m.running && m.st.State == "running" && m.st.PID != os.Getpid() &&
		(m.st.Mode == "background" || m.st.Mode == "foreground")
}

// confirmEnable asks before the service is installed and started, and says
// which bypass running now it replaces.
func (m *Model) confirmEnable() tea.Cmd {
	if !m.svc.Supported {
		m.note = i18n.T("service.unsupported")
		return nil
	}
	changes := []string{i18n.T("shell.enable.service"), i18n.T("shell.enable.boot"), i18n.T("shell.enable.rules")}
	switch {
	case m.running:
		changes = append(changes, i18n.T("shell.enable.move_here"))
	case m.otherRun():
		changes = append(changes, i18n.T("shell.enable.move", i18n.Enum("mode", m.st.Mode), m.st.PID))
	}
	m.dialog = &confirm{
		title:   i18n.T("shell.enable.title"),
		changes: changes,
		undo:    "sudo splitdpi disable",
		yes:     (*Model).enable,
	}
	return nil
}

// enable stops the bypass that runs outside the service and waits until it
// has exited, then installs, enables and starts the service. Enable saves
// run mode boot once the service runs, and only then.
func (m *Model) enable() tea.Cmd {
	m.note = i18n.T("tui.wiz.enabling")
	b, ctx, stopOwn, other := m.b, m.ctx, m.stopOwnRun(), m.otherRun()
	return func() tea.Msg {
		stopOwn()
		if other {
			if err := b.Stop(); err != nil && !errors.Is(err, ops.ErrNotRunning) {
				return enableMsg{err}
			}
		}
		_, err := b.Enable(ctx)
		return enableMsg{err}
	}
}

// enabled takes Enable's result. Quitting moved the bypass here to the
// service: it quits once the service runs.
func (m Model) enabled(err error) (Model, tea.Cmd) {
	if err != nil {
		// A failed move to the service stays, so that the failure is seen.
		m.note, m.quitting = errText(err), false
		return m, m.refresh()
	}
	// Status and Settings enable too; they leave the wizard alone.
	if m.page == pgScan && m.wiz.step == wDone {
		m.finishWizard()
	}
	m.cfg.RunMode, m.note = config.RunBoot, i18n.T("service.enabled")
	if m.quitting {
		m.toService = true
		return m, tea.Quit
	}
	return m, batch(m.refresh(), m.restartLogs())
}

// confirmDisable asks before the service is removed for run mode mode.
func (m *Model) confirmDisable(mode config.RunMode) tea.Cmd {
	changes := []string{i18n.T("shell.disable.service")}
	if m.st.State == "running" && m.st.Mode == "service" {
		changes = append(changes, i18n.T("shell.disable.stops"))
	}
	m.dialog = &confirm{
		title:   i18n.T("shell.disable.title"),
		changes: append(changes, i18n.T("shell.disable.mode", runModeName(mode))),
		undo:    "sudo splitdpi enable",
		yes:     func(m *Model) tea.Cmd { return m.disable(mode) },
	}
	return nil
}

// disable removes the service, then saves run mode mode.
func (m *Model) disable(mode config.RunMode) tea.Cmd {
	m.note = i18n.T("shell.disable.working")
	b, ctx := m.b, m.ctx
	return func() tea.Msg {
		if err := b.Disable(ctx); err != nil && !errors.Is(err, ops.ErrNotEnabled) {
			return runModeMsg{mode: mode, err: err}
		}
		return runModeMsg{mode: mode, disabled: true, err: b.SetRunMode(mode)}
	}
}

// bootUnset is run mode boot without the service it asks for.
func (m Model) bootUnset() bool {
	return m.runMode() == config.RunBoot && m.svc.Supported && !m.svc.Enabled
}

// bootValue is whether SplitDPI starts at boot, as the init system has it,
// with a warning while run mode boot lacks the service.
func bootValue(m Model) string {
	switch {
	case m.svc.Enabled:
		return goodStyle.Render(i18n.T("boot.yes"))
	case !m.svc.Supported:
		return mutedStyle.Render(i18n.T("boot.unsupported"))
	case m.bootUnset():
		return warnStyle.Render(i18n.T("boot.not_set_up"))
	}
	return mutedStyle.Render(i18n.T("boot.no"))
}

// bootActions set the missing service up; otherwise the run mode decides,
// as on its own row.
func bootActions(m Model) []action {
	if m.bootUnset() {
		return []action{m.setUpBootAction("enter")}
	}
	return runModeActions(m)
}

// setUpBootAction sets up the service that run mode boot asks for, as key.
func (m Model) setUpBootAction(key string) action {
	return action{Key: key, Label: i18n.T("boot.set_up"), CLI: "sudo splitdpi enable", Do: (*Model).confirmEnable}
}

// quitMove is where b in the quit prompt moves the bypass run here: to the
// service in run mode boot, else to the background.
func (m Model) quitMove() config.RunMode {
	if m.runMode() == config.RunBoot && m.svc.Supported {
		return config.RunBoot
	}
	return config.RunBackground
}

// printService tells, once the screen is back to normal, that quitting left
// the bypass to the service.
func (m Model) printService(w io.Writer) {
	if m.toService {
		fmt.Fprintln(w, i18n.T("service.enabled"))
	}
}
