package tui

import (
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/KadirBerkpolat1/SplitDPI/internal/i18n"
	"github.com/KadirBerkpolat1/SplitDPI/internal/ops"
)

// The strategy screen: choose a preset strategy (`splitdpi strategy set`).
type strategyScreen struct {
	list   []ops.StrategyChoice
	cursor int
}

type strategySetMsg struct {
	ch      ops.StrategyChoice
	running bool
	err     error
}

func (m *Model) openStrategy() tea.Cmd {
	m.scr, m.focus, m.note = scrStrategy, 0, ""
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

func (m Model) strategyActions() []action {
	s := m.strat
	cli := ""
	if s.cursor < len(s.list) {
		cli = "sudo splitdpi strategy set " + s.list[s.cursor].Strategy.ID
	}
	return []action{
		{Key: "enter", Label: i18n.T("tui.act.choose"), CLI: cli, Do: (*Model).chooseStrategy},
		{Key: "esc", Label: i18n.T("tui.act.back"), Do: func(m *Model) tea.Cmd { *m = m.leave(); return nil }},
		{Key: "q", Label: i18n.T("tui.act.quit"), Do: (*Model).quit},
	}
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

func (m Model) strategyUpdate(msg tea.Msg) (Model, tea.Cmd, bool) {
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

// strategyKey moves the cursor; false means not handled.
func (m Model) strategyKey(s string) (Model, tea.Cmd, bool) {
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

func (m Model) strategyView() string {
	s := m.strat
	lines := []string{titleStyle.Render(i18n.T("tui.strategy_screen.title")), "", i18n.T("tui.strategy_screen.pick"), ""}
	width := 0
	for _, ch := range s.list {
		width = max(width, len(ch.Strategy.ID))
	}
	// Footer: blank line, up to two key lines, CLI echo, note.
	room := max(m.height-len(lines)-5, 1)
	start := 0
	if len(s.list) > room {
		start = min(max(s.cursor-room/2, 0), len(s.list)-room)
	}
	for i := start; i < min(start+room, len(s.list)); i++ {
		ch := s.list[i]
		current := m.cfg.Strategy != nil && m.cfg.Strategy.ID == ch.Strategy.ID
		lines = append(lines, cursorMark(i == s.cursor)+checkbox(current)+" "+ch.Strategy.ID+
			strings.Repeat(" ", width-len(ch.Strategy.ID))+mutedStyle.Render("  "+ch.From()))
	}
	return strings.Join(lines, "\n")
}
