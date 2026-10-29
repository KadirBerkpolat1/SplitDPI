package tui

import (
	"context"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/KadirBerkpolat1/SplitDPI/internal/i18n"
)

func logsActions() []action {
	return []action{
		{Key: "esc", Label: i18n.T("tui.act.back"), CLI: "sudo splitdpi logs -f", Do: func(m *Model) tea.Cmd {
			*m = m.leave()
			return nil
		}},
		{Key: "q", Label: i18n.T("tui.act.quit"), Do: (*Model).quit},
	}
}

// openLogs streams the service log (like `splitdpi logs -f`) into the same
// buffer that receives the output of a bypass run from this TUI.
func (m *Model) openLogs() tea.Cmd {
	ctx, cancel := context.WithCancel(m.ctx)
	m.scr, m.focus, m.logCancel = scrLogs, 0, cancel
	b, w := m.b, m.logs
	stream := func() tea.Msg {
		b.Logs(ctx, true, w)
		return logEndMsg{}
	}
	return tea.Batch(stream, m.every(500*time.Millisecond, func(time.Time) tea.Msg { return logTickMsg{} }))
}

func (m Model) logsView() string {
	lines := []string{titleStyle.Render(i18n.T("tui.logs")), ""}
	room := m.height - len(lines) - 5 // footer
	got := m.logs.Last(max(room, 1))
	if len(got) == 0 {
		got = []string{mutedStyle.Render(i18n.T("tui.logs_empty"))}
	}
	return strings.Join(append(lines, got...), "\n")
}
