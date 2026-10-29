package tui

import (
	"context"
	"maps"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/KadirBerkpolat1/SplitDPI/internal/i18n"
)

func (m Model) bypassOn() bool { return m.running || m.st.State == "running" }

func (m Model) statusActions() []action {
	var acts []action
	if m.bypassOn() {
		acts = append(acts, action{Key: "s", Label: i18n.T("tui.act.stop"), CLI: "sudo splitdpi stop", Do: (*Model).stop})
	} else {
		acts = append(acts, action{Key: "r", Label: i18n.T("tui.act.run"), CLI: "sudo splitdpi run", Do: (*Model).runNow})
	}
	return append(acts,
		action{Key: "w", Label: i18n.T("tui.act.setup"), CLI: "sudo splitdpi scan", Do: (*Model).openWizard},
		action{Key: "t", Label: i18n.T("tui.act.strategy"), CLI: "sudo splitdpi strategy list", Do: (*Model).openStrategy},
		action{Key: "a", Label: i18n.T("tui.act.apps"), CLI: "sudo splitdpi apps list", Do: (*Model).openApps},
		action{Key: "p", Label: i18n.T("tui.act.probe"), CLI: "sudo splitdpi probe", Do: (*Model).probeApps},
		action{Key: "l", Label: i18n.T("tui.act.logs"), CLI: "sudo splitdpi logs -f", Do: (*Model).openLogs},
		action{Key: "q", Label: i18n.T("tui.act.quit"), Do: (*Model).quit},
	)
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

func (m Model) statusView() string {
	lines := []string{titleStyle.Render("splitdpi"), ""}

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
	lines = append(lines, m.row(i18n.T("tui.state"), state))

	if s := m.cfg.Strategy; s != nil {
		lines = append(lines, m.row(i18n.T("tui.strategy"), s.ID+mutedStyle.Render("  "+s.Source)))
	} else {
		lines = append(lines, m.row(i18n.T("tui.strategy"), warnStyle.Render(i18n.T("tui.strategy_none"))))
	}
	if m.st.Engine != "" {
		lines = append(lines, m.row(i18n.T("tui.engine"), "zapret2 "+m.st.Engine))
	}
	apps := i18n.T("tui.none")
	if len(m.cfg.Apps) > 0 {
		apps = strings.Join(m.cfg.Apps, ", ")
	}
	lines = append(lines, m.row(i18n.T("tui.apps"), apps))
	if m.cfg.DNSOn() {
		lines = append(lines, m.row(i18n.T("tui.dns"), warnStyle.Render(i18n.T("tui.dns_on"))))
	}
	for _, id := range slices.Sorted(maps.Keys(m.probe)) {
		verdict := goodStyle.Render(i18n.T("tui.reachable"))
		for _, r := range m.probe[id] {
			if !r.OK {
				verdict = badStyle.Render(i18n.T("tui.blocked"))
			}
		}
		lines = append(lines, m.row(i18n.T("tui.probe"), id+"  "+verdict))
	}
	if m.st.LastError != "" {
		lines = append(lines, m.row(i18n.T("tui.last_error"), badStyle.Render(m.st.LastError)))
	}
	if m.stErr != nil {
		lines = append(lines, badStyle.Render(m.stErr.Error()))
	}
	return strings.Join(lines, "\n")
}
