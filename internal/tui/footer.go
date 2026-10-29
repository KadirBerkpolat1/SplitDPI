package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/KadirBerkpolat1/SplitDPI/internal/i18n"
)

// One accent, used for the title, the focused action and the CLI echo; the
// rest is the terminal's own colors. Lip Gloss drops colors on terminals
// without them and when NO_COLOR is set.
var (
	accent = lipgloss.AdaptiveColor{Light: "25", Dark: "74"}
	muted  = lipgloss.AdaptiveColor{Light: "243", Dark: "245"}
	good   = lipgloss.AdaptiveColor{Light: "28", Dark: "71"}
	warn   = lipgloss.AdaptiveColor{Light: "130", Dark: "179"}
	bad    = lipgloss.AdaptiveColor{Light: "124", Dark: "167"}

	titleStyle   = lipgloss.NewStyle().Bold(true).Foreground(accent)
	labelStyle   = lipgloss.NewStyle().Foreground(muted).Width(12)
	mutedStyle   = lipgloss.NewStyle().Foreground(muted)
	focusStyle   = lipgloss.NewStyle().Bold(true).Underline(true).Foreground(accent)
	commandStyle = lipgloss.NewStyle().Foreground(accent)
	goodStyle    = lipgloss.NewStyle().Foreground(good)
	warnStyle    = lipgloss.NewStyle().Foreground(warn)
	badStyle     = lipgloss.NewStyle().Foreground(bad)
)

// footer lists the screen's keys, echoes the focused action's CLI command,
// and shows the last action's result or a pending question.
func (m Model) footer() string {
	acts := m.actions()
	keys := make([]string, len(acts))
	for i, a := range acts {
		k := "[" + a.Key + "] " + a.Label
		if i == m.focus {
			k = focusStyle.Render(k)
		}
		keys[i] = k
	}
	lines := append([]string{""}, packKeys(keys, m.width)...)
	if m.focus < len(acts) && acts[m.focus].CLI != "" {
		lines = append(lines, mutedStyle.Render("$ ")+commandStyle.Render(acts[m.focus].CLI))
	} else {
		lines = append(lines, "")
	}
	switch {
	case m.confirmQuit:
		lines = append(lines, warnStyle.Render(i18n.T("tui.confirm_quit")))
	case m.note != "":
		lines = append(lines, m.note)
	}
	return strings.Join(lines, "\n")
}

// packKeys lays "[k] Label" items out in lines of at most width columns,
// never splitting an item.
func packKeys(keys []string, width int) []string {
	var lines []string
	cur := ""
	for _, k := range keys {
		switch {
		case cur == "":
			cur = k
		case lipgloss.Width(cur)+2+lipgloss.Width(k) > width:
			lines, cur = append(lines, cur), k
		default:
			cur += "  " + k
		}
	}
	return append(lines, cur)
}

// row is one "label  value" line of a screen; a long value wraps under itself.
func (m Model) row(label, value string) string {
	return lipgloss.JoinHorizontal(lipgloss.Top,
		labelStyle.Render(label),
		lipgloss.NewStyle().Width(max(m.width-labelStyle.GetWidth(), 20)).Render(value))
}
