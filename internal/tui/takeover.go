package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/KadirBerkpolat1/SplitDPI/internal/i18n"
	"github.com/KadirBerkpolat1/SplitDPI/internal/ops"
)

// Another zapret install on Durum: a notice while it runs or starts on boot,
// and z switches to SplitDPI (`splitdpi takeover --yes`).

type (
	foreignMsg  struct{ found []ops.Foreign }
	takeoverMsg struct {
		lines []string
		err   error
	}
)

// inTheWay: f runs or starts on boot, so it fights SplitDPI for the traffic.
func inTheWay(f ops.Foreign) bool { return f.Running || f.Service.Enabled }

// canTakeOver: there is a service to stop or keep from starting. A process
// running outside a service is the user's to stop.
func canTakeOver(f ops.Foreign) bool {
	return inTheWay(f) && !(f.Running && f.Service.Name == "")
}

func (m Model) detectForeign() tea.Cmd {
	b, ctx, w := m.b, m.ctx, m.logs
	return func() tea.Msg {
		found, err := b.DetectForeign(ctx)
		if err != nil {
			fmt.Fprintln(w, err.Error())
		}
		var out []ops.Foreign
		for _, f := range found {
			if inTheWay(f) {
				out = append(out, f)
			}
		}
		return foreignMsg{out}
	}
}

func (m Model) takeoverAction() (action, bool) {
	for _, f := range m.foreign {
		if canTakeOver(f) {
			return action{Key: "z", Label: i18n.T("takeover_tui.act"), CLI: "sudo splitdpi takeover --yes", Do: (*Model).confirmTakeover}, true
		}
	}
	return action{}, false
}

// confirmTakeover lists what ops.Takeover will change for each install.
func (m *Model) confirmTakeover() tea.Cmd {
	var kinds, changes []string
	var todo []ops.Foreign
	for _, f := range m.foreign {
		if !canTakeOver(f) {
			continue
		}
		todo = append(todo, f)
		kinds = append(kinds, f.Kind)
		if f.Hostlist != "" {
			changes = append(changes, i18n.T("takeover_tui.import", f.Hostlist))
		}
		if f.Running {
			changes = append(changes, i18n.T("takeover_tui.stop", f.Service.Name, string(f.Service.Init)))
		}
		if f.Service.Enabled {
			changes = append(changes, i18n.T("takeover_tui.boot", f.Service.Name))
		}
	}
	if len(todo) == 0 {
		return nil
	}
	m.dialog = &confirm{
		title:   i18n.T("takeover_tui.title", strings.Join(kinds, ", ")),
		changes: append(changes, i18n.T("takeover_tui.untouched")),
		undo:    "sudo splitdpi revert",
		yes:     func(m *Model) tea.Cmd { return m.takeover(todo) },
	}
	return nil
}

func (m *Model) takeover(todo []ops.Foreign) tea.Cmd {
	m.note = ""
	b, ctx, w := m.b, m.ctx, m.logs
	return func() tea.Msg {
		var lines []string
		for _, f := range todo {
			r, err := b.Takeover(ctx, f)
			var did []string
			if len(r.Added) > 0 || (f.Hostlist != "" && err == nil) {
				did = append(did, i18n.N("takeover.imported", len(r.Added), f.Hostlist))
			}
			if len(r.Skipped) > 0 {
				did = append(did, i18n.N("takeover_tui.skipped", len(r.Skipped)))
				for _, s := range r.Skipped {
					fmt.Fprintf(w, "%s:%d: %s\n", f.Hostlist, s.Line, s.Text)
				}
			}
			if r.Stopped {
				did = append(did, i18n.T("takeover.did_stop", f.Service.Name))
			}
			if r.Disabled {
				did = append(did, i18n.T("takeover.did_disable", f.Service.Name))
			}
			for _, l := range did {
				fmt.Fprintln(w, f.Kind+": "+l)
			}
			lines = append(lines, did...)
			if err != nil {
				fmt.Fprintln(w, f.Kind+": "+err.Error())
				return takeoverMsg{lines, err}
			}
		}
		return takeoverMsg{lines: lines}
	}
}

func (m Model) takeoverUpdate(msg tea.Msg) (Model, tea.Cmd, bool) {
	switch msg := msg.(type) {
	case foreignMsg:
		m.foreign = msg.found
		return m, nil, true
	case takeoverMsg:
		m.takenOver = msg.lines
		m.note = i18n.T("takeover_tui.done")
		if msg.err != nil {
			m.note = errText(msg.err)
		}
		return m, tea.Batch(m.refresh(), m.detectForeign()), true
	}
	return m, nil, false
}

// takeoverView is the notice about other installs, then the last takeover's report.
func (m Model) takeoverView(w int) []string {
	notice := warnStyle.Width(w)
	var lines []string
	for _, f := range m.foreign {
		text := i18n.T("takeover_tui.installed", f.Kind)
		switch {
		case f.Running && f.Service.Name == "":
			text = i18n.T("takeover_tui.manual", f.Kind)
		case f.Running:
			text = i18n.T("takeover_tui.running", f.Kind)
		}
		lines = append(lines, strings.Split(notice.Render(text), "\n")...)
	}
	if len(m.takenOver) > 0 {
		lines = append(lines, titleStyle.Render(i18n.T("takeover_tui.report")))
		item := lipgloss.NewStyle().Width(w - 3)
		for _, l := range m.takenOver {
			for i, s := range strings.Split(item.Render(l), "\n") {
				prefix := "   "
				if i == 0 {
					prefix = " " + m.g.bullet + " "
				}
				lines = append(lines, prefix+s)
			}
		}
	}
	if len(lines) > 0 {
		lines = append(lines, "")
	}
	return lines
}
