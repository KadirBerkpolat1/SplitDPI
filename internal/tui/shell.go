package tui

import (
	"slices"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/KadirBerkpolat1/SplitDPI/internal/i18n"
)

// One accent (teal) for the active tab, the keys and the cursor; three
// state colors for running, attention and failure; a muted gray for
// secondary text and the frame. Each color has a dark- and a light-terminal
// variant with 256- and 16-color fallbacks. Lip Gloss drops colors on
// terminals without them and when NO_COLOR is set.
var (
	accent = tone("#5fbfb9", "73", "#0b6b66", "23", "6")
	good   = tone("#8cc98a", "114", "#2e7a32", "28", "2")
	warn   = tone("#e2b35e", "179", "#8a5300", "94", "3")
	bad    = tone("#e7807f", "174", "#b02a22", "124", "1")
	muted  = tone("#878f9d", "245", "#5b6472", "242", "8")
	line   = tone("#4a5260", "239", "#b2b8c2", "250", "8")
	tabBg  = tone("#1f3a3a", "23", "#d5eeec", "195", "")

	titleStyle   = lipgloss.NewStyle().Bold(true).Foreground(accent)
	keyStyle     = lipgloss.NewStyle().Bold(true).Foreground(accent)
	labelStyle   = lipgloss.NewStyle().Foreground(muted).Width(15)
	mutedStyle   = lipgloss.NewStyle().Foreground(muted)
	focusStyle   = lipgloss.NewStyle().Bold(true).Foreground(accent)
	commandStyle = lipgloss.NewStyle().Foreground(accent)
	goodStyle    = lipgloss.NewStyle().Foreground(good)
	warnStyle    = lipgloss.NewStyle().Foreground(warn)
	badStyle     = lipgloss.NewStyle().Foreground(bad)
	neutralStyle = lipgloss.NewStyle().Foreground(muted)
	groupStyle   = lipgloss.NewStyle().Bold(true)
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
	// maxHints is how many of a page's keys the hint bar shows at most; the
	// help lists them all.
	maxHints = 6
)

func (m Model) tooSmall() bool { return m.width < minWidth || m.height < minHeight }

// span is a stretch of one screen line that a click presses key on.
type span struct {
	y, x0, x1 int
	key       string
}

// pillKey is the span key of the pill: a click starts or stops the bypass.
const pillKey = "\x00pill"

// screen is the layout of the parts around the page.
type screen struct {
	head      string
	headSpans []span
	toast     string
	bar       string
	barSpans  []span
	frameH    int // the frame's height, borders included
}

func (m Model) layout() screen {
	var s screen
	s.head, s.headSpans = m.header()
	s.toast = m.toast()
	s.bar, s.barSpans = m.hintBar()
	s.frameH = m.height - 2 - lipgloss.Height(s.toast)
	if s.toast == "" {
		s.frameH = m.height - 2
	}
	return s
}

// pageSize is the width and height View gives the page.
func (m Model) pageSize() (int, int) { return m.width - 4, m.layout().frameH - 2 }

// View is the tab bar, the page in its frame, the toast and the hint bar;
// a popup covers the frame and the toast.
func (m Model) View() string {
	if m.tooSmall() {
		return m.smallView()
	}
	s := m.layout()
	var body string
	if m.showDisclaimer() {
		body = m.frame(m.width, s.frameH, i18n.T("disclaimer.title"), m.disclaimerView(m.width-4))
	} else {
		p := pages[m.page]
		body = m.frame(m.width, s.frameH, p.Hint(), p.View(m, m.width-4, s.frameH-2))
	}
	if s.toast != "" {
		body += "\n" + s.toast
	}
	if box, x, y, ok := m.placedPopup(s); ok {
		body = overlay(body, box, x, y-1)
	}
	return s.head + "\n" + body + "\n" + s.bar
}

// smallView keeps a question and quitting usable on a terminal below the
// minimum size: the question's title and keys, each line cut to the width.
func (m Model) smallView() string {
	var lines []string
	if p, ok := m.popupBox(); ok {
		lines = append(lines, p.lines[0])
		lines = append(lines, p.lines[len(p.lines)-p.tail:]...)
	} else {
		lines = []string{i18n.T("shell.too_small", minWidth, minHeight), hint("q", i18n.T("tui.act.quit"))}
	}
	lines = lines[:min(len(lines), max(m.height, 1))]
	for i, l := range lines {
		lines[i] = ansi.Truncate(l, m.width, "")
	}
	return strings.Join(lines, "\n")
}

// header is the name, the tabs and the bypass state. The name gives way
// first when it does not fit, then the pill's words.
func (m Model) header() (string, []span) {
	pill := m.pill(true)
	if m.showDisclaimer() {
		left := titleStyle.Render(appName)
		return left + strings.Repeat(" ", max(m.width-lipgloss.Width(left)-lipgloss.Width(pill), 1)) + pill, nil
	}
	tabs := make([]string, len(pages))
	width := 0
	for i, p := range pages {
		tabs[i] = m.tab(i, p.Title())
		width += lipgloss.Width(tabs[i])
	}
	name := titleStyle.Render(appName) + "  "
	if lipgloss.Width(name)+width+lipgloss.Width(pill)+1 > m.width {
		name = ""
	}
	if lipgloss.Width(name)+width+lipgloss.Width(pill)+1 > m.width {
		pill = m.pill(false)
	}
	var b strings.Builder
	var spans []span
	b.WriteString(name)
	x := lipgloss.Width(name)
	for i, t := range tabs {
		w := lipgloss.Width(t)
		spans = append(spans, span{0, x, x + w, strconv.Itoa(i + 1)})
		b.WriteString(t)
		x += w
	}
	gap := max(m.width-x-lipgloss.Width(pill), 1)
	b.WriteString(strings.Repeat(" ", gap))
	spans = append(spans, span{0, x + gap, x + gap + lipgloss.Width(pill), pillKey})
	b.WriteString(pill)
	return b.String(), spans
}

// tab is the i'th tab: its number to press and its title.
func (m Model) tab(i int, title string) string {
	n := strconv.Itoa(i + 1)
	switch {
	case i != m.page:
		return " " + mutedStyle.Render(n) + " " + title + " "
	case m.g.ascii:
		return "[" + n + " " + titleStyle.Render(title) + "]"
	}
	on := lipgloss.NewStyle().Background(tabBg)
	return on.Render(" ") + on.Foreground(accent).Render(n) + on.Render(" ") + on.Bold(true).Foreground(accent).Render(title) + on.Render(" ")
}

// pill is the bypass state in words, or only its dot when words is false.
// A setup scan shows itself, as on Status, and so does a restart under way.
func (m Model) pill(words bool) string {
	dot, label, style := m.g.dotOff, i18n.T("shell.pill.stopped"), mutedStyle
	switch {
	case m.wiz.scanning:
		dot, label, style = m.g.dot, i18n.T("fix_arayuz.pill_scanning"), warnStyle
	case m.restarting || m.st.State == "restarting":
		dot, label, style = m.g.dot, i18n.T("fix_arayuz.pill_restarting"), warnStyle
	case m.bypassOn():
		dot, label, style = m.g.dot, i18n.T("shell.pill.running"), goodStyle
	case m.st.State == "failed":
		dot, label, style = m.g.dot, i18n.T("shell.pill.failed"), badStyle
	}
	if !words {
		return style.Bold(true).Render(dot)
	}
	return style.Bold(true).Render(dot + " " + label)
}

// pillAction is what a click on the pill does: Status's stop, or its start
// in the run mode, with the confirmation that start asks for; nothing while
// they are held.
func (m Model) pillAction() (action, bool) {
	switch {
	case m.held():
	case m.bypassOn():
		return m.stopAction(), true
	case m.canStart() && m.cfg.Strategy != nil:
		return m.startAction(), true
	}
	return action{}, false
}

// frame draws content in a w x h box with title on its top border.
func (m Model) frame(w, h int, title, content string) string {
	border := lipgloss.NewStyle().Foreground(line)
	b := m.g.border
	title = ansi.Truncate(title, max(w-7, 0), m.g.ellipsis)
	fill := max(w-5-lipgloss.Width(title), 0)
	top := border.Render(b.TopLeft+b.Top) + " " + mutedStyle.Render(title) + " " + border.Render(strings.Repeat(b.Top, fill)+b.TopRight)
	if title == "" {
		top = border.Render(b.TopLeft + strings.Repeat(b.Top, max(w-2, 0)) + b.TopRight)
	}
	inner := lipgloss.NewStyle().Padding(0, 1).Width(w - 2).Height(max(h-2, 0)).MaxHeight(max(h-2, 0)).Render(content)
	box := lipgloss.NewStyle().Border(b, false, true, true, true).BorderForeground(line).Render(inner)
	return top + "\n" + box
}

// actions are the keys of what the screen shows: the first-start notice or
// the selected page.
func (m Model) actions() []action {
	if m.showDisclaimer() {
		return disclaimerActions()
	}
	return pages[m.page].Actions(m)
}

// toastLines is how many lines the toast may take.
const toastLines = 3

// toast is the last action's result, under the frame.
func (m Model) toast() string {
	if m.note == "" {
		return ""
	}
	return lipgloss.NewStyle().Width(m.width).MaxHeight(toastLines).Padding(0, 1).Foreground(accent).
		Render(fitNote(m.note, m.width-2))
}

// fitNote drops the first lines of note, each whole, while it wraps to more
// than toastLines lines at width; the last line always stays. A note puts
// what to do now last, and a command on a line of its own: lipgloss would
// break a command at a hyphen, and one cut in two cannot be copied.
func fitNote(note string, width int) string {
	lines := strings.Split(note, "\n")
	wrap := lipgloss.NewStyle().Width(width)
	for len(lines) > 1 && lipgloss.Height(wrap.Render(strings.Join(lines, "\n"))) > toastLines {
		lines = lines[1:]
	}
	return strings.Join(lines, "\n")
}

func hint(key, label string) string { return keyStyle.Render(key) + " " + label }

// restartKey restarts a running bypass with pending changes from any page.
const restartKey = "R"

// restartAction is R while the running bypass has changes waiting and can be
// restarted here.
func (m Model) restartAction() (action, bool) {
	if !m.pending || !m.canRestart() {
		return action{}, false
	}
	return action{Key: restartKey, Label: i18n.T("tui.act.restart"), CLI: m.restartCLI(), Do: (*Model).restart}, true
}

// hintBar is one line: the restart key while changes wait, then the page's
// keys in order while they fit (at most maxHints), then ? and q at the
// right. It never wraps; what does not fit is in the help.
func (m Model) hintBar() (string, []span) {
	var left []action
	if a, ok := m.restartAction(); ok && !m.showDisclaimer() {
		left = append(left, a)
	}
	for _, a := range m.actions() {
		if a.Key != "" && a.Key != restartKey {
			left = append(left, a)
		}
	}
	var right []action
	if !m.showDisclaimer() && !m.typing() {
		right = []action{{Key: "?", Label: i18n.T("shell.help.key")}, {Key: "q", Label: i18n.T("tui.act.quit")}}
	}
	item := func(a action) string { return hint(keyName(a.Key), a.Label) }
	rw := 0
	for i, a := range right {
		if i > 0 {
			rw += 2
		}
		rw += lipgloss.Width(item(a))
	}
	room := m.width - 1 - rw
	if rw > 0 {
		room -= 3
	}
	var b strings.Builder
	var spans []span
	x, y := 1, m.height-1
	b.WriteString(" ")
	shown := 0
	for _, a := range left {
		t := item(a)
		w := lipgloss.Width(t)
		gap := 0
		if shown > 0 {
			gap = 2
		}
		if shown == maxHints || x+gap+w > room {
			break
		}
		b.WriteString(strings.Repeat(" ", gap) + t)
		spans = append(spans, span{y, x + gap, x + gap + w, a.Key})
		x += gap + w
		shown++
	}
	if rw > 0 {
		gap := max(m.width-1-rw-x, 2)
		b.WriteString(strings.Repeat(" ", gap))
		x += gap
		for i, a := range right {
			if i > 0 {
				b.WriteString("  ")
				x += 2
			}
			t := item(a)
			spans = append(spans, span{y, x, x + lipgloss.Width(t), a.Key})
			b.WriteString(t)
			x += lipgloss.Width(t)
		}
	}
	return ansi.Truncate(b.String(), m.width, ""), spans
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

// scrollMore is scroll for a list to choose from: a muted "↑ n more" or
// "↓ n more" line counts the items cut off above or below. first holds each
// item's first line (nil: one line per item); cursor is an item.
func (m Model) scrollMore(lines []string, first []int, cursor, room, width int) []string {
	at := cursor
	if first != nil {
		at = 0
		if cursor < len(first) {
			at = first[cursor]
		}
	}
	if len(lines) <= room || room < 3 {
		return scroll(lines, at, room, width)
	}
	body := room - 2
	start := min(max(at-body/2, 0), len(lines)-body)
	if start == 0 {
		body++
	} else if start+body == len(lines) {
		body++
		start--
	}
	above, below := start, len(lines)-start-body
	if first != nil {
		// Start on an item, not under one cut off above; the cursor's item
		// starts at or after the next one.
		if i, ok := slices.BinarySearch(first, start); !ok && i < len(first) {
			body = min(body, len(lines)-first[i])
			start = first[i]
		}
		count := func() {
			above, below = 0, 0
			for _, l := range first {
				switch {
				case l < start:
					above++
				case l >= start+body:
					below++
				}
			}
		}
		count()
		// Only headings cut off above: show them instead of "↑ 0 more".
		if above == 0 && start > 0 && at < room-1 {
			start, body = 0, room-1
			count()
		}
	}
	out := scroll(lines[start:start+body], 0, body, width)
	if start > 0 {
		out = append([]string{m.more(m.g.up, above, width)}, out...)
	}
	if start+body < len(lines) {
		out = append(out, m.more(m.g.down, below, width))
	}
	return out
}

// more is the muted line that counts n entries cut off in the arrow's direction.
func (m Model) more(arrow string, n, width int) string {
	pad := strings.Repeat(" ", lipgloss.Width(m.g.cursor)+1)
	return ansi.Truncate(pad+mutedStyle.Render(arrow+" "+i18n.T("list_scroll.more", n)), width, "")
}

// cursorMark is the cursor of a list, or as many blanks.
func (m Model) cursorMark(on bool) string {
	if on {
		return focusStyle.Render(m.g.cursor) + " "
	}
	return strings.Repeat(" ", lipgloss.Width(m.g.cursor)+1)
}

// listMark is the cursor of a page's list.
func (m Model) listMark(on bool) string { return m.cursorMark(on) }

// mark shows whether a toggle is on, or the bypass applies to an entry.
func (m Model) mark(on bool) string {
	if on {
		return goodStyle.Render(m.g.on)
	}
	return mutedStyle.Render(m.g.off)
}

// overlay draws box over base with its top left corner at column x, line y.
func overlay(base, box string, x, y int) string {
	lines := strings.Split(base, "\n")
	over := strings.Split(box, "\n")
	bw := lipgloss.Width(box)
	for i, o := range over {
		if y+i < 0 || y+i >= len(lines) {
			continue
		}
		l := lines[y+i]
		left := ansi.Truncate(l, x, "")
		left += strings.Repeat(" ", max(x-ansi.StringWidth(left), 0)) // an empty toast line is shorter than x
		lines[y+i] = left + ansi.ResetStyle + o + ansi.ResetStyle + ansi.TruncateLeft(l, x+bw, "")
	}
	return strings.Join(lines, "\n")
}
