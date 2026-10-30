package tui

import (
	"slices"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

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
	keyStyle     = lipgloss.NewStyle().Bold(true).Foreground(accent)
	labelStyle   = lipgloss.NewStyle().Foreground(muted).Width(12)
	mutedStyle   = lipgloss.NewStyle().Foreground(muted)
	focusStyle   = lipgloss.NewStyle().Bold(true).Underline(true).Foreground(accent)
	commandStyle = lipgloss.NewStyle().Foreground(accent)
	goodStyle    = lipgloss.NewStyle().Foreground(good)
	warnStyle    = lipgloss.NewStyle().Foreground(warn)
	badStyle     = lipgloss.NewStyle().Foreground(bad)
)

const (
	appName   = "SplitDPI"
	minWidth  = 80
	minHeight = 24
	menuWidth = 16
	menuTop   = 2 // the header line, then the menu's top border
)

func (m Model) tooSmall() bool { return m.width < minWidth || m.height < minHeight }

// View is the header, the menu and the page side by side, and the footer.
func (m Model) View() string {
	if m.tooSmall() {
		return ansi.Truncate(i18n.T("shell.too_small", minWidth, minHeight), m.width, "")
	}
	foot := m.footer()
	h := m.height - 1 - lipgloss.Height(foot)
	var body string
	if m.showDisclaimer() {
		body = m.frame(m.width, h, true, titleStyle.Render(i18n.T("disclaimer.title"))+"\n\n"+m.disclaimerView(m.width-4))
	} else {
		body = lipgloss.JoinHorizontal(lipgloss.Top, m.menuBox(h), m.pageBox(m.width-menuWidth, h))
	}
	switch {
	case m.dialog != nil:
		body = overlay(body, m.dialogView(), menuWidth, m.width)
	case m.ask != nil:
		body = overlay(body, m.dnsAskView(), menuWidth, m.width)
	case m.help:
		body = overlay(body, m.helpView(), menuWidth, m.width)
	}
	return m.header() + "\n" + body + "\n" + foot
}

// header is the name and version left, the bypass state right.
func (m Model) header() string {
	left := titleStyle.Render(m.g.logo+" "+appName) + " " + mutedStyle.Render(m.version)
	right := m.pill()
	return left + strings.Repeat(" ", max(m.width-lipgloss.Width(left)-lipgloss.Width(right), 1)) + right
}

func (m Model) pill() string {
	switch {
	case m.bypassOn():
		return goodStyle.Bold(true).Render(m.g.dot + " " + i18n.T("shell.pill.running"))
	case m.st.State == "failed":
		return badStyle.Bold(true).Render(m.g.dot + " " + i18n.T("shell.pill.failed"))
	}
	return mutedStyle.Bold(true).Render(m.g.dot + " " + i18n.T("shell.pill.stopped"))
}

// frame draws content in a w x h box; the focused box has the accent border.
func (m Model) frame(w, h int, focused bool, content string) string {
	color := muted
	if focused {
		color = accent
	}
	inner := lipgloss.NewStyle().Padding(0, 1).Width(w - 2).Height(h - 2).MaxHeight(h - 2).Render(content)
	return lipgloss.NewStyle().Border(m.g.border).BorderForeground(color).Render(inner)
}

func (m Model) menuBox(h int) string {
	lines := make([]string, len(pages))
	for i, p := range pages {
		lines[i] = "  " + p.Title()
		if i == m.page {
			lines[i] = titleStyle.Render(m.g.cursor + " " + p.Title())
		}
	}
	return m.frame(menuWidth, h, !m.inPage, strings.Join(lines, "\n"))
}

// pageBox is the selected page under its title and hint.
func (m Model) pageBox(w, h int) string {
	p := pages[m.page]
	width := w - 4
	title := ansi.Truncate(titleStyle.Render(p.Title())+mutedStyle.Render("  "+m.g.sep+" "+p.Hint()), width, "")
	return m.frame(w, h, m.inPage, title+"\n\n"+p.View(m, width, h-4))
}

// footer lists the page's keys and the global ones, echoes the focused
// action's CLI command, and shows the last action's result or a pending
// question.
func (m Model) footer() string {
	var acts []action
	if m.showDisclaimer() {
		acts = disclaimerActions()
	} else {
		acts = pages[m.page].Actions(m)
	}
	var keys []string
	for i, a := range acts {
		if a.Key == "" {
			continue
		}
		k := keyStyle.Render(keyName(a.Key)) + " " + a.Label
		if m.inPage && i == m.focus {
			k = focusStyle.Render(keyName(a.Key) + " " + a.Label)
		}
		keys = append(keys, k)
	}
	// A page that binds esc is typing or busy; its own keys are all that apply.
	if !m.showDisclaimer() && !slices.ContainsFunc(acts, func(a action) bool { return a.Key == "esc" }) {
		nav := m.g.menuKeys
		if m.inPage {
			nav = keyName("esc")
		}
		keys = append(keys, hint(nav, i18n.T("shell.to_menu")), hint("?", i18n.T("shell.help.key")), hint("q", i18n.T("tui.act.quit")))
	}
	lines := packKeys(keys, m.width)
	if m.focus < len(acts) && acts[m.focus].CLI != "" {
		lines = append(lines, ansi.Truncate(mutedStyle.Render("$ ")+commandStyle.Render(acts[m.focus].CLI), m.width, "…"))
	} else {
		lines = append(lines, "")
	}
	note := lipgloss.NewStyle().Width(m.width).MaxHeight(3)
	switch {
	case m.confirmQuit:
		lines = append(lines, note.Render(warnStyle.Render(i18n.T("background_tui.quit"))+"\n"+
			hint(i18n.T("confirm_keys.yes"), i18n.T("background_tui.quit_stop"))+"   "+
			hint("b", i18n.T("background_tui.quit_background"))+"   "+
			hint(i18n.T("confirm_keys.no"), i18n.T("background_tui.quit_cancel"))))
	case m.note != "":
		lines = append(lines, note.Render(m.note))
	}
	return strings.Join(lines, "\n")
}

func hint(key, label string) string { return keyStyle.Render(key) + " " + mutedStyle.Render(label) }

// keyName is how a key is written on screen.
func keyName(k string) string {
	switch k {
	case "enter", "esc", "space", "tab":
		return i18n.T("shell.key." + k)
	}
	return k
}

// packKeys lays "k Label" items out in lines of at most width columns,
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

// row is one "label  value" line of a page width wide; a long value wraps
// under itself.
func row(width int, label, value string) string {
	return lipgloss.JoinHorizontal(lipgloss.Top,
		labelStyle.Render(label),
		lipgloss.NewStyle().Width(max(width-labelStyle.GetWidth(), 20)).Render(value))
}

// scroll keeps at most room of lines, with the cursor's line in view, each
// cut to width so that no line wraps.
func scroll(lines []string, cursor, room, width int) []string {
	room = max(room, 1)
	if len(lines) > room {
		start := min(max(cursor-room/2, 0), len(lines)-room)
		lines = lines[start : start+room]
	}
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = ansi.Truncate(l, width, "")
	}
	return out
}

func (m Model) cursorMark(on bool) string {
	if on {
		return focusStyle.Render(m.g.cursor) + " "
	}
	return strings.Repeat(" ", lipgloss.Width(m.g.cursor)+1)
}

// listMark is the cursor of a page's list. It shows only while the page has
// focus, so that the menu's cursor is the one active marker otherwise.
func (m Model) listMark(on bool) string { return m.cursorMark(on && m.inPage) }

// mark shows whether a toggle is on, or the bypass applies to an entry.
func (m Model) mark(on bool) string {
	if on {
		return goodStyle.Render(m.g.on)
	}
	return mutedStyle.Render(m.g.off)
}

// popupText is the text width of the help and the confirmation dialog, which
// cover the page and leave the menu in sight.
func (m Model) popupText() int { return min(60, m.width-menuWidth-4) }

func (m Model) popup(content string) string {
	return lipgloss.NewStyle().Border(m.g.border).BorderForeground(accent).Padding(0, 1).
		Width(m.popupText() + 2).Render(content)
}

// overlay draws box over base, centered between the columns left and right.
func overlay(base, box string, left, right int) string {
	lines := strings.Split(base, "\n")
	over := strings.Split(box, "\n")
	bw := lipgloss.Width(box)
	x, y := left+max((right-left-bw)/2, 0), max((len(lines)-len(over))/2, 0)
	for i, o := range over {
		if y+i >= len(lines) {
			break
		}
		l := lines[y+i]
		lines[y+i] = ansi.Truncate(l, x, "") + ansi.ResetStyle + o + ansi.TruncateLeft(l, x+bw, "")
	}
	return strings.Join(lines, "\n")
}

func (m Model) helpView() string {
	p := pages[m.page]
	keyCol := lipgloss.NewStyle().Width(12)
	labelCol := lipgloss.NewStyle().Width(m.popupText() - keyCol.GetWidth())
	line := func(k, label string) string {
		return lipgloss.JoinHorizontal(lipgloss.Top, keyCol.Render(keyStyle.Render(k)), labelCol.Render(label))
	}
	lines := []string{titleStyle.Render(i18n.T("shell.help.page", p.Title()))}
	for _, a := range p.Actions(m) {
		if a.Key != "" {
			lines = append(lines, line(keyName(a.Key), a.Label))
		}
	}
	lines = append(lines, "", titleStyle.Render(i18n.T("shell.help.global")),
		line(m.g.menuKeys, i18n.T("shell.help.arrows")),
		line(keyName("tab")+" "+keyName("enter"), i18n.T("shell.help.open")),
		line(keyName("esc"), i18n.T("shell.help.back")),
		line(i18n.T("shell.help.click"), i18n.T("shell.help.click_does")),
		line("?", i18n.T("shell.help.toggle")),
		line("q", i18n.T("tui.act.quit")),
		"", mutedStyle.Render(i18n.T("shell.help.close")))
	return m.popup(strings.Join(lines, "\n"))
}
