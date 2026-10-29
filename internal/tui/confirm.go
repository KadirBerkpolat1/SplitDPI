package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/KadirBerkpolat1/SplitDPI/internal/i18n"
)

// confirm asks before an action that changes the system, saying what will
// change and how to undo it. undoHead replaces the "to undo it" line when
// undo is not a command that reverses the action.
type confirm struct {
	title    string
	changes  []string
	undoHead string
	undo     string
	yes      func(m *Model) tea.Cmd
}

func (m Model) dialogKey(s string) (tea.Model, tea.Cmd) {
	switch s {
	case "y", "enter":
		yes := m.dialog.yes
		m.dialog = nil
		cmd := yes(&m)
		return m, cmd
	case "n", "esc":
		m.dialog = nil
	}
	return m, nil
}

func (m Model) dialogView() string {
	c := m.dialog
	item := lipgloss.NewStyle().Width(m.popupText() - 3)
	lines := []string{titleStyle.Render(c.title), "", i18n.T("shell.confirm.changes")}
	for _, ch := range c.changes {
		for i, l := range strings.Split(item.Render(ch), "\n") {
			prefix := "   "
			if i == 0 {
				prefix = " " + m.g.bullet + " "
			}
			lines = append(lines, prefix+l)
		}
	}
	head := i18n.T("shell.confirm.undo")
	if c.undoHead != "" {
		head = c.undoHead
	}
	lines = append(lines, "", head, "   "+commandStyle.Render(c.undo), "",
		hint("y", i18n.T("shell.confirm.yes"))+"   "+hint("n", i18n.T("shell.confirm.no")))
	return m.popup(strings.Join(lines, "\n"))
}
