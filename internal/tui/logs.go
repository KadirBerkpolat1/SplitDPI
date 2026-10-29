package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/KadirBerkpolat1/SplitDPI/internal/i18n"
)

// The logs page: the service log (like `splitdpi logs -f`) and the output
// of a bypass run from this TUI, in one buffer.
type logsPage struct{}

type (
	logTickMsg struct{}
	logEndMsg  struct{ stream int }
)

func (logsPage) Title() string { return i18n.T("shell.menu.logs") }
func (logsPage) Hint() string  { return i18n.T("shell.hint.logs") }

func (logsPage) Actions(Model) []action {
	return []action{{CLI: "sudo splitdpi logs -f"}}
}

func (logsPage) Key(m Model, _ tea.KeyMsg, _ string) (Model, tea.Cmd, bool) { return m, nil, false }

// Open starts the log stream unless it runs; it runs until it ends or the
// TUI quits, so the lines are not read twice.
func (logsPage) Open(m *Model) tea.Cmd {
	var stream tea.Cmd
	if m.logCancel == nil {
		ctx, cancel := context.WithCancel(m.ctx)
		m.logCancel = cancel
		m.logStream++
		b, w, id := m.b, m.logs, m.logStream
		stream = func() tea.Msg {
			if err := b.Logs(ctx, true, w); err != nil && ctx.Err() == nil {
				fmt.Fprintln(w, err.Error())
			}
			return logEndMsg{id}
		}
	}
	var tick tea.Cmd
	if !m.logTicking {
		m.logTicking = true
		tick = m.logTick()
	}
	return batch(stream, tick)
}

// restartLogs replaces a running log stream after a background run started:
// a stream picks the journal or LogFile once, by the run that was on then.
func (m *Model) restartLogs() tea.Cmd {
	if m.logCancel == nil {
		return nil // the next Open starts one
	}
	m.logCancel()
	m.logCancel = nil
	if m.page != pgLogs {
		return nil
	}
	return logsPage{}.Open(m)
}

func (m Model) logTick() tea.Cmd {
	return m.every(500*time.Millisecond, func(time.Time) tea.Msg { return logTickMsg{} })
}

// Update redraws the page while it is shown.
func (logsPage) Update(m Model, msg tea.Msg) (Model, tea.Cmd, bool) {
	switch msg := msg.(type) {
	case logTickMsg:
		if m.page == pgLogs {
			return m, m.logTick(), true
		}
		m.logTicking = false
		return m, nil, true
	case logEndMsg:
		if msg.stream == m.logStream && m.logCancel != nil {
			m.logCancel()
			m.logCancel = nil // the next Open tries again
		}
		return m, nil, true
	}
	return m, nil, false
}

func (logsPage) View(m Model, w, h int) string {
	got := m.logs.Last(max(h, 1))
	if len(got) == 0 {
		return mutedStyle.Render(i18n.T("tui.logs_empty"))
	}
	return strings.Join(scroll(got, len(got)-1, h, w), "\n")
}
