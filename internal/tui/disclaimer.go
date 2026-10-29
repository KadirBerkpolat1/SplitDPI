package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/KadirBerkpolat1/SplitDPI/internal/i18n"
)

// The neutral first-start notice, shown once before anything else.

type disclaimerMsg struct{ err error }

func (m Model) showDisclaimer() bool { return !m.cfg.DisclaimerAccepted && m.stErr == nil }

func disclaimerActions() []action {
	return []action{
		{Key: "enter", Label: i18n.T("disclaimer.accept"), Do: (*Model).acceptDisclaimer},
		{Key: "q", Label: i18n.T("tui.act.quit"), Do: (*Model).quit},
	}
}

func (m *Model) acceptDisclaimer() tea.Cmd {
	b := m.b
	return func() tea.Msg { return disclaimerMsg{b.AcceptDisclaimer()} }
}

func (m Model) disclaimerView(width int) string {
	return lipgloss.NewStyle().Width(min(width, 72)).Render(i18n.T("disclaimer.body"))
}
