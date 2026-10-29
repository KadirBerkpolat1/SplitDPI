package tui

import (
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/KadirBerkpolat1/SplitDPI/internal/i18n"
)

// The settings page: one row per setting, each with its own actions.
type settingsPage struct{}

type settingsScreen struct{ cursor int }

type setting struct {
	label   func() string
	value   func(m Model) string
	actions func(m Model) []action
}

var settings = []setting{
	{label: func() string { return i18n.T("shell.settings.lang") }, value: langValue, actions: langActions},
}

// The languages offered; the others still work with --lang.
var langs = []string{"en", "tr"}

type langMsg struct{ err error }

func (settingsPage) Title() string       { return i18n.T("shell.menu.settings") }
func (settingsPage) Hint() string        { return i18n.T("shell.hint.settings") }
func (settingsPage) Open(*Model) tea.Cmd { return nil }

func (settingsPage) Actions(m Model) []action { return settings[m.settings.cursor].actions(m) }

func (settingsPage) Key(m Model, _ tea.KeyMsg, s string) (Model, tea.Cmd, bool) {
	n := len(settings)
	switch s {
	case "up", "k":
		m.settings.cursor = (m.settings.cursor + n - 1) % n
		return m, nil, n > 1
	case "down", "j":
		m.settings.cursor = (m.settings.cursor + 1) % n
		return m, nil, n > 1
	}
	return m, nil, false
}

func (settingsPage) Update(m Model, msg tea.Msg) (Model, tea.Cmd, bool) {
	l, ok := msg.(langMsg)
	if !ok {
		return m, nil, false
	}
	m.note = i18n.T("shell.settings.lang_saved")
	if l.err != nil {
		m.note = errText(l.err)
	}
	return m, nil, true
}

func (settingsPage) View(m Model, w, _ int) string {
	labelW := 0
	for _, s := range settings {
		labelW = max(labelW, lipgloss.Width(s.label()))
	}
	lines := make([]string, len(settings))
	for i, s := range settings {
		label := lipgloss.NewStyle().Width(labelW + 2).Render(s.label())
		lines[i] = m.cursorMark(i == m.settings.cursor) + label + s.value(m)
	}
	return strings.Join(scroll(lines, m.settings.cursor, len(lines), w), "\n")
}

func langValue(Model) string { return i18n.T("shell.lang." + i18n.Lang()) }

// langActions switch to the next offered language; it applies at once and
// is saved as the config's lang.
func langActions(Model) []action {
	next := langs[(slices.Index(langs, i18n.Lang())+1)%len(langs)]
	return []action{{Key: "space", Label: i18n.T("shell.settings.lang_to", i18n.T("shell.lang."+next)), Do: func(m *Model) tea.Cmd {
		i18n.Init(next)
		b := m.b
		return func() tea.Msg { return langMsg{b.SetLang(next)} }
	}}}
}
