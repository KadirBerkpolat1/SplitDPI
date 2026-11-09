package tui

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/KadirBerkpolat1/SplitDPI/internal/config"
	"github.com/KadirBerkpolat1/SplitDPI/internal/i18n"
	"github.com/KadirBerkpolat1/SplitDPI/internal/ops"
)

// Starting at boot. Only the init system says whether SplitDPI starts at
// boot; run mode boot asks for it. Choosing boot sets the service up at once,
// and a bypass that runs now moves to it; leaving boot removes the service.

type (
	enableMsg struct{ err error }
	// serviceDoneMsg carries the result of a service change, msg, and
	// whether it failed.
	serviceDoneMsg struct {
		msg tea.Msg
		err error
	}
)

// serviceOp is a service change under way: enabling, which takes over the
// bypass running now, or disabling. Quitting waits for it, and so does Run
// when the program ends otherwise: cut short, it could leave no bypass
// running.
type serviceOp struct {
	done chan struct{}
	msg  tea.Msg // the result, once done is closed
	err  error
}

// changeService runs change as the service change under way.
func (m *Model) changeService(change func() (tea.Msg, error)) tea.Cmd {
	op := &serviceOp{done: make(chan struct{})}
	m.svcOp = op
	return func() tea.Msg {
		defer close(op.done)
		op.msg, op.err = change()
		return serviceDoneMsg{op.msg, op.err}
	}
}

// serviceDone takes the result of the service change under way. A quit
// asked for meanwhile goes on now, as q would: it asks first while this TUI
// still runs the bypass. A change that took the run here over has ended it,
// but that run's runDoneMsg may still be on the way: then that one quits.
// After a failure the TUI stays, so that the failure is seen.
func (m Model) serviceDone(d serviceDoneMsg) (tea.Model, tea.Cmd) {
	m.svcOp = nil
	next, cmd := m.Update(d.msg)
	m = next.(Model)
	if !m.quitAfter {
		return m, cmd
	}
	m.quitAfter = false
	switch {
	case d.err != nil:
		return m, cmd
	case m.running && m.runEnded():
		m.quitting, m.quitNote = true, m.note
		m.stopAll()
		return m, nil
	}
	if quit := m.quit(); quit != nil {
		m.quitNote = m.note
		return m, quit
	}
	return m, cmd
}

// runEnded is whether the bypass this TUI runs has ended, before its
// runDoneMsg has arrived.
func (m Model) runEnded() bool {
	select {
	case <-m.runFinished:
		return true
	default:
		return false
	}
}

// waitService waits for the service change the program ended in the middle
// of, by a signal or a lost terminal, and says how it ended.
func (m Model) waitService(w io.Writer) {
	op := m.svcOp
	if op == nil {
		return
	}
	select {
	case <-op.done:
	default:
		fmt.Fprintln(w, i18n.T("tui.service_wait"))
		<-op.done
	}
	if op.err != nil {
		fmt.Fprintln(w, errText(op.err))
		return
	}
	switch msg := op.msg.(type) {
	case enableMsg:
		fmt.Fprintln(w, i18n.T("service.enabled"))
	case runModeMsg:
		fmt.Fprintln(w, i18n.T("run_mode.disabled", runModeName(msg.mode)))
	}
}

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
	if m.svcOp != nil {
		m.note = i18n.T("tui.service_busy")
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

// enable sets the service up in place of the bypass running now. Enable
// stops that bypass, this window's through stopHere, only once every check
// has passed and the service is installed, and starts it again in the
// background if the service does not start. It saves run mode boot once the
// service runs, and only then.
func (m *Model) enable() tea.Cmd {
	m.note = i18n.T("tui.wiz.enabling")
	var stopHere func()
	if m.running {
		stopHere = m.stopOwnRun()
	}
	b, ctx, w := m.b, m.ctx, m.logs
	return m.changeService(func() (tea.Msg, error) {
		_, err := b.Enable(ctx, stopHere)
		if err != nil {
			fmt.Fprintln(w, err)
		}
		return enableMsg{err}, err
	})
}

// enabled takes Enable's result.
func (m Model) enabled(err error) (Model, tea.Cmd) {
	if err != nil {
		m.note = errText(err)
		return m, m.refresh()
	}
	// Status and Settings enable too; they leave the wizard alone.
	if m.page == pgScan && m.wiz.step == wDone {
		m.finishWizard()
	}
	m.cfg.RunMode, m.note = config.RunBoot, i18n.T("service.enabled")
	return m, batch(m.refresh(), m.restartLogs())
}

// takeoverText words a TakeoverError as the CLI does, on the note line: the
// first line of why the service did not take over, and what runs the bypass
// now. All of the error is in the log.
func takeoverText(e ops.TakeoverError) string {
	var stop ops.StopRunError
	var parts []string
	switch {
	case errors.As(e.Err, &stop) && errors.Is(stop.Err, ops.ErrStopTimeout):
		parts = append(parts, i18n.T("service.stop_slow", i18n.Enum("mode", stop.Mode), stop.PID))
	case errors.As(e.Err, &stop):
		parts = append(parts, i18n.T("service.stop_failed", i18n.Enum("mode", stop.Mode), stop.PID, firstLine(stop.Err)))
	case errors.Is(e.Err, ops.ErrServiceNotStarted):
		parts = append(parts, i18n.T("service.not_started"))
	default:
		parts = append(parts, i18n.T("service.start_failed", firstLine(e.Err)))
	}
	if e.Undo != nil {
		parts = append(parts, i18n.T("service.undo_failed", firstLine(e.Undo)))
	}
	switch {
	case e.Stopped == "":
	case e.Running != "":
		parts = append(parts, i18n.T("service.takeover_kept", i18n.Enum("mode", e.Running)))
	case e.Stopping != "":
		parts = append(parts, i18n.T("service.takeover_stopping", i18n.Enum("mode", e.Stopping)))
	case e.RestartErr != nil:
		parts = append(parts, i18n.T("service.takeover_down"))
	default:
		parts = append(parts, i18n.T("service.takeover_restarted", e.Restarted))
	}
	return strings.Join(parts, " ")
}

func firstLine(err error) string {
	first, _, _ := strings.Cut(err.Error(), "\n")
	return first
}

// unsafeText words an UnsafeBinaryError as the CLI does.
func unsafeText(e ops.UnsafeBinaryError) string {
	reason := i18n.T("service.unsafe_"+e.Reason, e.At)
	if e.Path == e.Install {
		return i18n.T("service.unsafe_default", e.Path, reason)
	}
	return i18n.T("service.unsafe", e.Path, reason, e.Path, e.Install)
}

// confirmDisable asks before the service is removed for run mode mode.
func (m *Model) confirmDisable(mode config.RunMode) tea.Cmd {
	if m.svcOp != nil {
		m.note = i18n.T("tui.service_busy")
		return nil
	}
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
	return m.changeService(func() (tea.Msg, error) {
		if err := b.Disable(ctx); err != nil && !errors.Is(err, ops.ErrNotEnabled) {
			return runModeMsg{mode: mode, err: err}, err
		}
		err := b.SetRunMode(mode)
		return runModeMsg{mode: mode, disabled: true, err: err}, err
	})
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

// printQuitNote tells, once the screen is back to normal, what the service
// change that quitting waited for did.
func (m Model) printQuitNote(w io.Writer) {
	if m.quitNote != "" {
		fmt.Fprintln(w, m.quitNote)
	}
}
