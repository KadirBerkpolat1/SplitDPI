package tui

import (
	"context"
	"maps"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/KadirBerkpolat1/SplitDPI/internal/i18n"
	"github.com/KadirBerkpolat1/SplitDPI/internal/probe"
)

// The status page: is the bypass running and with what; run, stop, probe.
type statusPage struct{}

type (
	runDoneMsg struct{ err error }
	probeMsg   struct {
		res map[string][]probe.Result
		err error
	}
)

func (statusPage) Title() string                                              { return i18n.T("shell.menu.status") }
func (statusPage) Hint() string                                               { return i18n.T("shell.hint.status") }
func (statusPage) Open(*Model) tea.Cmd                                        { return nil }
func (statusPage) Key(m Model, _ tea.KeyMsg, _ string) (Model, tea.Cmd, bool) { return m, nil, false }

func (m Model) bypassOn() bool { return m.running || m.st.State == "running" }

// Actions offer the setup first while there is no strategy to run with.
func (statusPage) Actions(m Model) []action {
	var acts []action
	if m.cfg.Strategy == nil {
		acts = append(acts, action{Key: "w", Label: i18n.T("tui.act.setup"), CLI: "sudo splitdpi scan", Do: func(m *Model) tea.Cmd {
			m.page, m.inPage = pgScan, true
			return m.openWizard()
		}})
	}
	if m.bypassOn() {
		acts = append(acts, action{Key: "s", Label: i18n.T("tui.act.stop"), CLI: "sudo splitdpi stop", Do: (*Model).stop})
	} else {
		acts = append(acts, action{Key: "r", Label: i18n.T("tui.act.run"), CLI: "sudo splitdpi run", Do: (*Model).runNow})
	}
	return append(acts, action{Key: "p", Label: i18n.T("tui.act.probe"), CLI: "sudo splitdpi probe", Do: (*Model).probeApps})
}

func (statusPage) Update(m Model, msg tea.Msg) (Model, tea.Cmd, bool) {
	switch msg := msg.(type) {
	case runDoneMsg:
		m.running, m.runCancel = false, nil
		if msg.err != nil {
			m.note = errText(msg.err)
		}
		if m.quitting {
			return m, tea.Quit, true
		}
		return m, m.refresh(), true
	case probeMsg:
		m.probe, m.note = msg.res, ""
		if msg.err != nil {
			m.note = errText(msg.err)
		}
		return m, nil, true
	}
	return m, nil, false
}

// runNow runs the bypass inside this process, like `splitdpi run` in a
// terminal: quitting the TUI stops it and removes the rules.
func (m *Model) runNow() tea.Cmd {
	if m.running {
		return nil
	}
	ctx, cancel := context.WithCancel(m.ctx)
	finished := make(chan struct{})
	m.running, m.runCancel, m.runFinished, m.note = true, cancel, finished, ""
	b := m.b
	return func() tea.Msg {
		defer close(finished)
		return runDoneMsg{b.Run(ctx, false)}
	}
}

func (m *Model) stop() tea.Cmd {
	if m.running {
		m.runCancel() // runDoneMsg follows once the rules are gone
		return nil
	}
	b := m.b
	return func() tea.Msg {
		if err := b.Stop(); err != nil {
			return probeMsg{err: err}
		}
		st, cfg, err := b.Status()
		return statusMsg{st, cfg, err}
	}
}

func (m *Model) probeApps() tea.Cmd {
	m.note = i18n.T("tui.probing")
	b, ctx, apps := m.b, m.ctx, m.cfg.Apps
	return func() tea.Msg {
		res, err := b.Probe(ctx, apps)
		return probeMsg{res, err}
	}
}

func (statusPage) View(m Model, w, _ int) string {
	var lines []string
	state := i18n.Enum("state", m.st.State)
	switch {
	case m.running:
		state = goodStyle.Render(i18n.T("tui.running_here"))
	case m.st.State == "running":
		state = goodStyle.Render(state) + mutedStyle.Render("  "+i18n.T("tui.process", i18n.Enum("mode", m.st.Mode), m.st.PID))
	case m.st.State == "failed":
		state = badStyle.Render(state)
	default:
		state = mutedStyle.Render(state)
	}
	lines = append(lines, row(w, i18n.T("tui.state"), state))

	if s := m.cfg.Strategy; s != nil {
		lines = append(lines, row(w, i18n.T("tui.strategy"), s.ID+mutedStyle.Render("  "+s.Source)))
	} else {
		lines = append(lines, row(w, i18n.T("tui.strategy"), warnStyle.Render(i18n.T("tui.strategy_none"))))
	}
	if m.st.Engine != "" {
		lines = append(lines, row(w, i18n.T("tui.engine"), "zapret2 "+m.st.Engine))
	}
	apps := i18n.T("tui.none")
	if len(m.cfg.Apps) > 0 {
		apps = strings.Join(m.cfg.Apps, ", ")
	}
	lines = append(lines, row(w, i18n.T("tui.apps"), apps))
	if m.cfg.DNS {
		lines = append(lines, row(w, i18n.T("tui.dns"), warnStyle.Render(i18n.T("tui.dns_on"))))
	}
	for _, id := range slices.Sorted(maps.Keys(m.probe)) {
		verdict := goodStyle.Render(i18n.T("tui.reachable"))
		for _, r := range m.probe[id] {
			if !r.OK {
				verdict = badStyle.Render(i18n.T("tui.blocked"))
			}
		}
		lines = append(lines, row(w, i18n.T("tui.probe"), id+"  "+verdict))
	}
	if m.st.LastError != "" {
		lines = append(lines, row(w, i18n.T("tui.last_error"), badStyle.Render(m.st.LastError)))
	}
	if m.stErr != nil {
		lines = append(lines, badStyle.Render(m.stErr.Error()))
	}
	return strings.Join(lines, "\n")
}
