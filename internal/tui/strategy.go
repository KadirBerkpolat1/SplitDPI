package tui

import (
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/KadirBerkpolat1/SplitDPI/internal/i18n"
	"github.com/KadirBerkpolat1/SplitDPI/internal/ops"
)

// The strategy page: choose a preset strategy (`splitdpi strategy set`).
type strategyPage struct{}

type strategyScreen struct {
	list   []ops.StrategyChoice
	cursor int
}

type strategySetMsg struct {
	ch      ops.StrategyChoice
	running bool
	err     error
}

func (strategyPage) Title() string { return i18n.T("shell.menu.strategy") }
func (strategyPage) Hint() string  { return i18n.T("shell.hint.strategy") }

func (strategyPage) Open(m *Model) tea.Cmd {
	list, cur, err := m.b.StrategyList()
	m.strat = strategyScreen{list: list}
	if err != nil {
		m.note = errText(err)
	}
	if cur != nil {
		m.strat.cursor = max(slices.IndexFunc(list, func(ch ops.StrategyChoice) bool { return ch.Strategy.ID == cur.ID }), 0)
	}
	return nil
}

func (strategyPage) Actions(m Model) []action {
	s := m.strat
	cli := ""
	if s.cursor < len(s.list) {
		cli = "sudo splitdpi strategy set " + s.list[s.cursor].Strategy.ID
	}
	return []action{{Key: "enter", Label: i18n.T("tui.act.choose"), CLI: cli, Do: (*Model).chooseStrategy}}
}

func (m *Model) chooseStrategy() tea.Cmd {
	s := m.strat
	if s.cursor >= len(s.list) {
		return nil
	}
	id, b := s.list[s.cursor].Strategy.ID, m.b
	return func() tea.Msg {
		ch, running, err := b.StrategySet(id)
		return strategySetMsg{ch, running, err}
	}
}

func (strategyPage) Update(m Model, msg tea.Msg) (Model, tea.Cmd, bool) {
	set, ok := msg.(strategySetMsg)
	if !ok {
		return m, nil, false
	}
	if set.err != nil {
		m.note = errText(set.err)
		return m, nil, true
	}
	m.note = i18n.T("strategy.set", set.ch.Strategy.ID, set.ch.From())
	if set.running {
		m.note += " " + i18n.T("strategy.restart")
	}
	return m, m.refresh(), true
}

// Key moves the cursor.
func (strategyPage) Key(m Model, _ tea.KeyMsg, s string) (Model, tea.Cmd, bool) {
	st := &m.strat
	n := len(st.list)
	switch s {
	case "up", "k":
		if n > 0 {
			st.cursor = (st.cursor + n - 1) % n
		}
		return m, nil, n > 0
	case "down", "j":
		if n > 0 {
			st.cursor = (st.cursor + 1) % n
		}
		return m, nil, n > 0
	}
	return m, nil, false
}

func (strategyPage) View(m Model, w, h int) string {
	s := m.strat
	head := strings.Split(lipgloss.NewStyle().Width(w).Render(i18n.T("tui.strategy_screen.pick")), "\n")
	head = append(head, "")
	width := 0
	for _, ch := range s.list {
		width = max(width, len(ch.Strategy.ID))
	}
	blank := strings.Repeat(" ", lipgloss.Width(m.g.on))
	lines := make([]string, len(s.list))
	for i, ch := range s.list {
		mark := blank
		if m.cfg.Strategy != nil && m.cfg.Strategy.ID == ch.Strategy.ID {
			mark = goodStyle.Render(m.g.on)
		}
		lines[i] = m.cursorMark(i == s.cursor) + mark + " " + ch.Strategy.ID +
			strings.Repeat(" ", width-len(ch.Strategy.ID)) + mutedStyle.Render("  "+ch.From())
	}
	return strings.Join(append(head, scroll(lines, s.cursor, h-len(head), w)...), "\n")
}
