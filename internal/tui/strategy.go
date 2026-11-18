package tui

import (
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/KadirBerkpolat1/SplitDPI/internal/config"
	"github.com/KadirBerkpolat1/SplitDPI/internal/i18n"
	"github.com/KadirBerkpolat1/SplitDPI/internal/ops"
)

// The second part of the setup page: choose a preset method by hand
// (`splitdpi strategy set`). A method a scan found is not a preset: it shows
// first, and choosing a preset over it asks first, since only another scan
// brings it back.
type strategyScreen struct {
	list   []ops.StrategyChoice
	cur    *config.Strategy // the method in use
	custom bool             // cur is not one of the presets: it is row 0
	cursor int
}

type strategySetMsg struct {
	ch      ops.StrategyChoice
	running bool
	err     error
}

// openStrategies opens the list on the method in use.
func (m *Model) openStrategies() tea.Cmd {
	list, cur, err := m.b.StrategyList()
	if err != nil {
		m.note = errText(err)
	}
	s := strategyScreen{list: list, cur: cur}
	if cur != nil {
		i := slices.IndexFunc(list, func(ch ops.StrategyChoice) bool { return ch.Strategy.ID == cur.ID })
		s.custom = i < 0
		s.cursor = max(i, 0)
	}
	m.strat = s
	m.wiz.step = wStrategy
	return nil
}

// rows is the list's length, the custom method included.
func (s strategyScreen) rows() int {
	if s.custom {
		return len(s.list) + 1
	}
	return len(s.list)
}

// choice is the preset under the cursor, if the cursor is on one.
func (s strategyScreen) choice() (ops.StrategyChoice, bool) {
	i := s.cursor
	if s.custom {
		i--
	}
	if i < 0 || i >= len(s.list) {
		return ops.StrategyChoice{}, false
	}
	return s.list[i], true
}

func (m Model) strategyActions() []action {
	back := action{Key: "esc", Label: i18n.T("tui.act.back"), Do: (*Model).resetWizard}
	ch, ok := m.strat.choice()
	if !ok {
		return []action{back}
	}
	return []action{{Key: "enter", Label: i18n.T("tui.act.choose"), CLI: "sudo splitdpi strategy set " + ch.Strategy.ID, Do: (*Model).chooseStrategy}, back}
}

// chooseStrategy saves the preset under the cursor; over a method a scan
// found, it asks first.
func (m *Model) chooseStrategy() tea.Cmd {
	ch, ok := m.strat.choice()
	if !ok {
		return nil
	}
	if cur := m.strat.cur; cur != nil && cur.ID == ch.Strategy.ID {
		return nil
	}
	set := func(m *Model) tea.Cmd {
		id, b := ch.Strategy.ID, m.b
		return func() tea.Msg {
			ch, running, err := b.StrategySet(id)
			return strategySetMsg{ch, running, err}
		}
	}
	if !m.strat.custom {
		return set(m)
	}
	m.dialog = &confirm{
		title:    i18n.T("tui.strategy_screen.replace_title"),
		changes:  []string{i18n.T("tui.strategy_screen.replace_use", ch.From(), ch.Strategy.ID), i18n.T("tui.strategy_screen.replace_lost", m.strat.cur.ID)},
		undoHead: i18n.T("tui.strategy_screen.replace_undo"),
		undo:     "sudo splitdpi scan",
		yes:      set,
	}
	return nil
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
	m.note = i18n.T("tui.note.method", set.ch.From())
	if set.running {
		m.note = m.withChange(m.note)
	}
	if m.wiz.step == wStrategy {
		m.openStrategies()
	}
	return m, m.refresh(), true
}

// strategyKey moves the cursor; esc is the back action.
func (m Model) strategyKey(s string) (Model, tea.Cmd, bool) {
	st := &m.strat
	n := st.rows()
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

// strategyView lists the methods by whom they are for, the one in use
// marked, each with its id muted.
func (m Model) strategyView(w, h int) string {
	s := m.strat
	head := strings.Split(lipgloss.NewStyle().Width(w).Render(i18n.T("tui.strategy_screen.pick")), "\n")
	head = append(head, "")
	nameW := 0
	for _, ch := range s.list {
		nameW = max(nameW, lipgloss.Width(ch.From()))
	}
	if s.custom {
		nameW = max(nameW, lipgloss.Width(i18n.T("tui.strategy_screen.custom")))
	}
	name := lipgloss.NewStyle().Width(min(nameW, w/2) + 2).MaxWidth(min(nameW, w/2) + 2)
	blank := strings.Repeat(" ", lipgloss.Width(m.g.on))
	var lines []string
	if s.custom {
		lines = append(lines, m.listMark(s.cursor == 0)+goodStyle.Render(m.g.on)+" "+name.Render(i18n.T("tui.strategy_screen.custom"))+mutedStyle.Render(s.cur.ID))
	}
	for i, ch := range s.list {
		mark := blank
		if s.cur != nil && s.cur.ID == ch.Strategy.ID {
			mark = goodStyle.Render(m.g.on)
		}
		at := i
		if s.custom {
			at++
		}
		lines = append(lines, m.listMark(at == s.cursor)+mark+" "+name.Render(ch.From())+mutedStyle.Render(ch.Strategy.ID))
	}
	if len(lines) == 0 {
		lines = []string{mutedStyle.Render(i18n.T("tui.none"))}
	}
	return strings.Join(append(head, m.scrollMore(lines, nil, s.cursor, h-len(head), w)...), "\n")
}
