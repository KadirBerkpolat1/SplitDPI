package tui

import (
	"slices"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/KadirBerkpolat1/SplitDPI/internal/i18n"
)

// One accent, used for the title, the keys, the primary button and the CLI
// echo; three state colors; and the backgrounds of the dock's buttons, the
// menu's selected entry and the pill. Each color is truecolor with 256- and
// 16-color fallbacks, and has a variant for light terminals. Lip Gloss drops
// colors on terminals without them and when NO_COLOR is set.
var (
	accent    = tone("#f2a65a", "215", "#a5520f", "130", "3")
	good      = tone("#93c98a", "114", "#2e7d32", "28", "2")
	bad       = tone("#e5787f", "174", "#b3261e", "124", "1")
	neutral   = tone("#8a93a3", "246", "#596273", "242", "7")
	muted     = tone("#79818f", "244", "#5f6673", "243", "8")
	text      = tone("#d9dee8", "253", "#1e222c", "235", "7")
	selBg     = tone("#2c3343", "236", "#dde3ee", "254", "0")
	buttonBg  = tone("#252a36", "235", "#e6e9ef", "255", "0")
	primaryBg = tone("#3a2c1c", "94", "#f6e3cc", "223", "8")
	pillOnBg  = tone("#24412a", "22", "#dcefd8", "194", "0")
	pillOffBg = tone("#3a2a2c", "52", "#f6dcdc", "224", "0")
	warn      = lipgloss.AdaptiveColor{Light: "130", Dark: "179"}

	titleStyle   = lipgloss.NewStyle().Bold(true).Foreground(accent)
	keyStyle     = lipgloss.NewStyle().Bold(true).Foreground(accent)
	labelStyle   = lipgloss.NewStyle().Foreground(muted).Width(12)
	mutedStyle   = lipgloss.NewStyle().Foreground(muted)
	focusStyle   = lipgloss.NewStyle().Bold(true).Underline(true).Foreground(accent)
	commandStyle = lipgloss.NewStyle().Foreground(accent)
	goodStyle    = lipgloss.NewStyle().Foreground(good)
	warnStyle    = lipgloss.NewStyle().Foreground(warn)
	badStyle     = lipgloss.NewStyle().Foreground(bad)
	neutralStyle = lipgloss.NewStyle().Foreground(neutral)
)

func tone(dark, dark256, light, light256, ansi string) lipgloss.CompleteAdaptiveColor {
	return lipgloss.CompleteAdaptiveColor{
		Dark:  lipgloss.CompleteColor{TrueColor: dark, ANSI256: dark256, ANSI: ansi},
		Light: lipgloss.CompleteColor{TrueColor: light, ANSI256: light256, ANSI: ansi},
	}
}

const (
	appName   = "SplitDPI"
	minWidth  = 80
	minHeight = 24
	menuWidth = 16
	menuTop   = 2 // the header line, then the menu's top border
)

func (m Model) tooSmall() bool { return m.width < minWidth || m.height < minHeight }

// View is the header, the menu and the page side by side, the toast, the
// dock and the command line.
func (m Model) View() string {
	if m.tooSmall() {
		return ansi.Truncate(i18n.T("shell.too_small", minWidth, minHeight), m.width, "")
	}
	rows, _ := m.dock()
	toast := m.toast()
	foot := strings.Join(rows, "\n") + "\n" + m.cmdLine()
	h := m.height - 1 - lipgloss.Height(toast) - lipgloss.Height(foot)
	var body string
	if m.showDisclaimer() {
		body = m.frame(m.width, h, true, titleStyle.Render(i18n.T("disclaimer.title"))+"\n\n"+m.disclaimerView(m.width-4))
	} else {
		body = lipgloss.JoinHorizontal(lipgloss.Top, m.menuBox(h), m.pageBox(m.width-menuWidth, h))
	}
	var box string
	switch {
	case m.dialog != nil:
		box = m.dialogView()
	case m.ask != nil:
		box = m.dnsAskView()
	case m.help:
		box = m.helpView()
	}
	// A box taller than the page may cover the toast too, never the dock.
	top := body + "\n" + toast
	switch {
	case box == "":
	case lipgloss.Height(box) > h:
		top = overlay(top, box, menuWidth, m.width)
	default:
		top = overlay(body, box, menuWidth, m.width) + "\n" + toast
	}
	return m.header() + "\n" + top + "\n" + foot
}

// header is the name and version left, the bypass state right.
func (m Model) header() string {
	left := titleStyle.Render(m.g.logo+" "+appName) + " " + mutedStyle.Render(m.version)
	right := m.pill()
	return left + strings.Repeat(" ", max(m.width-lipgloss.Width(left)-lipgloss.Width(right), 1)) + right
}

// pill is the bypass state; a click on it starts or stops the bypass as the
// Status page does.
func (m Model) pill() string {
	label, fg, bg := m.g.dotOff+" "+i18n.T("shell.pill.stopped"), bad, pillOffBg
	switch {
	case m.bypassOn():
		label, fg, bg = m.g.dot+" "+i18n.T("shell.pill.running"), good, pillOnBg
	case m.st.State == "failed":
		label = m.g.dot + " " + i18n.T("shell.pill.failed")
	}
	s := lipgloss.NewStyle().Bold(true).Foreground(fg)
	if m.g.ascii {
		return s.Render(label)
	}
	return s.Background(bg).Padding(0, 1).Render(label)
}

// pillAction is what a click on the pill does: Status's stop, or its start
// in the run mode, with the confirmation that start asks for.
func (m Model) pillAction() (action, bool) {
	switch {
	case m.bypassOn():
		return m.stopAction(), true
	case m.canStart():
		return m.runAction(), true
	}
	return action{}, false
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
		switch {
		case i != m.page:
			lines[i] = "  " + p.Title()
		case m.g.ascii:
			lines[i] = titleStyle.Render(m.g.menuMark + " " + p.Title())
		default:
			lines[i] = lipgloss.NewStyle().Foreground(accent).Background(selBg).Render(m.g.menuMark) +
				lipgloss.NewStyle().Bold(true).Foreground(text).Background(selBg).Width(menuWidth-5).Render(" "+p.Title())
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

// actions are the keys of what the screen shows: the first-start notice or
// the selected page.
func (m Model) actions() []action {
	if m.showDisclaimer() {
		return disclaimerActions()
	}
	return pages[m.page].Actions(m)
}

// toast is the last action's result or a pending question, over the dock.
func (m Model) toast() string {
	note := lipgloss.NewStyle().Width(m.width).MaxHeight(3)
	switch {
	case m.confirmQuit:
		return note.Render(warnStyle.Render(i18n.T("background_tui.quit")) + "\n" +
			hint(i18n.T("confirm_keys.yes"), i18n.T("background_tui.quit_stop")) + "   " +
			hint("b", i18n.T("background_tui.quit_background")) + "   " +
			hint(i18n.T("confirm_keys.no"), i18n.T("background_tui.quit_cancel")))
	case m.note != "":
		return note.Foreground(accent).Render(m.note)
	}
	return ""
}

// cmdLine echoes the CLI command of the focused action, the first one
// unless tab moved the focus.
func (m Model) cmdLine() string {
	acts := m.actions()
	if m.focus < len(acts) && acts[m.focus].CLI != "" {
		return ansi.Truncate(mutedStyle.Render("$ ")+commandStyle.Render(acts[m.focus].CLI), m.width, "…")
	}
	return ""
}

func hint(key, label string) string { return keyStyle.Render(key) + " " + mutedStyle.Render(label) }

// button is one cell of the dock: a click on it does what its key does.
type button struct {
	key, label       string
	primary, focused bool
	global           bool // ? and q, which leave the focus where it is
	row, x, width    int
}

// dock lays the screen's actions out as buttons from the left, the first
// one as the primary, with ? and q at the right of the first row. Buttons
// that do not fit go on to further rows whole; a label is never cut.
func (m Model) dock() ([]string, []button) {
	acts := m.actions()
	var btns []button
	for i, a := range acts {
		if a.Key != "" {
			btns = append(btns, button{key: a.Key, label: a.Label, primary: len(btns) == 0, focused: m.inPage && i == m.focus})
		}
	}
	// A page that binds esc is typing or busy; its own keys are all that apply.
	var globals []button
	if !m.showDisclaimer() && !slices.ContainsFunc(acts, func(a action) bool { return a.Key == "esc" }) {
		globals = []button{
			{key: "?", label: i18n.T("shell.help.key"), global: true},
			{key: "q", label: i18n.T("tui.act.quit"), global: true},
		}
	}
	room := m.width
	if len(globals) > 0 {
		gx := m.width + 1
		for i := len(globals) - 1; i >= 0; i-- {
			globals[i].width = globals[i].cells()
			gx -= globals[i].width + 1
			globals[i].x = gx
		}
		room = gx - 2
	}
	row, end := 0, 0
	for i := range btns {
		b := &btns[i]
		b.width = b.cells()
		x := end
		if x > 0 {
			x++
		}
		limit := m.width
		if row == 0 {
			limit = room
		}
		if x+b.width > limit && (end > 0 || row == 0) {
			row, x = row+1, 0
		}
		b.row, b.x, end = row, x, x+b.width
	}
	all := append(btns, globals...)
	rows := make([]string, row+1)
	ends := make([]int, row+1)
	for _, b := range all {
		rows[b.row] += strings.Repeat(" ", b.x-ends[b.row]) + m.buttonView(b)
		ends[b.row] = b.x + b.width
	}
	return rows, all
}

// cells is the width of a button: its key and label with a cell of padding,
// or the brackets, on either side.
func (b button) cells() int { return lipgloss.Width(keyName(b.key)) + lipgloss.Width(b.label) + 3 }

func (m Model) buttonView(b button) string {
	label := lipgloss.NewStyle()
	if b.focused {
		label = focusStyle
	}
	if m.g.ascii {
		return "[" + keyStyle.Render(keyName(b.key)) + " " + label.Render(b.label) + "]"
	}
	bg := buttonBg
	if b.primary {
		bg = primaryBg
	}
	if !b.focused {
		label = label.Foreground(text)
	}
	pad := lipgloss.NewStyle().Background(bg).Render(" ")
	return pad + keyStyle.Background(bg).Render(keyName(b.key)) + pad + label.Background(bg).Render(b.label) + pad
}

// buttonAt is the dock button at the cell x, y of the screen.
func (m Model) buttonAt(x, y int) (button, bool) {
	rows, btns := m.dock()
	r := y - (m.height - 1 - len(rows))
	for _, b := range btns {
		if b.row == r && x >= b.x && x < b.x+b.width {
			return b, true
		}
	}
	return button{}, false
}

// keyName is how a key is written on screen.
func keyName(k string) string {
	switch k {
	case "enter", "esc", "space", "tab":
		return i18n.T("shell.key." + k)
	}
	return k
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
