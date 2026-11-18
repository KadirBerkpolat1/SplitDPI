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

// isYes and isNo are the answers to a question: the language's letter, the
// English one, and enter or esc.
func isYes(s string) bool { return s == "enter" || s == "y" || s == i18n.T("confirm_keys.yes") }
func isNo(s string) bool  { return s == "esc" || s == "n" || s == i18n.T("confirm_keys.no") }

func (m Model) dialogKey(s string) (tea.Model, tea.Cmd) {
	switch {
	case isYes(s):
		yes := m.dialog.yes
		m.dialog = nil
		cmd := yes(&m)
		return m, cmd
	case isNo(s):
		m.dialog = nil
	}
	return m, nil
}

func (m Model) dialogBox() popupBox {
	c := m.dialog
	item := lipgloss.NewStyle().Width(m.popupText() - 3)
	p := popupBox{head: 1, tail: 1}
	p.lines = []string{titleStyle.Render(c.title), "", i18n.T("shell.confirm.changes")}
	for _, ch := range c.changes {
		for i, l := range strings.Split(item.Render(ch), "\n") {
			prefix := "   "
			if i == 0 {
				prefix = " " + m.g.bullet + " "
			}
			p.lines = append(p.lines, prefix+l)
		}
	}
	head := i18n.T("shell.confirm.undo")
	if c.undoHead != "" {
		head = c.undoHead
	}
	p.lines = append(p.lines, "")
	p.add(m, head)
	p.lines = append(p.lines, "   "+commandStyle.Render(c.undo), "")
	p.keys([2]string{i18n.T("confirm_keys.yes"), i18n.T("shell.confirm.yes")}, [2]string{i18n.T("confirm_keys.no"), i18n.T("shell.confirm.no")})
	return p
}
