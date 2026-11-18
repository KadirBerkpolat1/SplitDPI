package tui

import (
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/KadirBerkpolat1/SplitDPI/internal/config"
	"github.com/KadirBerkpolat1/SplitDPI/internal/i18n"
)

// popupBox is the content of a popup: the help, a confirmation, the scan's
// DNS question or the quit prompt. Its first head and last tail lines always
// show; the lines between scroll when the terminal is short. A click on a
// span presses its key; "#n" chooses row n of the popup's list.
type popupBox struct {
	lines      []string
	head, tail int
	spans      []span // y and x within lines
}

// popupText is the text width of a popup.
func (m Model) popupText() int { return min(64, m.width-8) }

// add appends text wrapped to the popup's width.
func (p *popupBox) add(m Model, text string) {
	p.lines = append(p.lines, strings.Split(lipgloss.NewStyle().Width(m.popupText()).Render(text), "\n")...)
}

// keys appends one line of key hints, each clickable.
func (p *popupBox) keys(items ...[2]string) {
	var b strings.Builder
	x := 0
	for i, it := range items {
		if i > 0 {
			b.WriteString("   ")
			x += 3
		}
		t := hint(keyName(it[0]), it[1])
		w := lipgloss.Width(t)
		p.spans = append(p.spans, span{len(p.lines), x, x + w, it[0]})
		b.WriteString(t)
		x += w
	}
	p.lines = append(p.lines, b.String())
}

// choice appends a clickable line that presses key.
func (p *popupBox) choice(text, key string) {
	p.spans = append(p.spans, span{len(p.lines), 0, lipgloss.Width(text), key})
	p.lines = append(p.lines, text)
}

// fit keeps the box within room lines: the lines between head and tail
// scroll from offset, with a line saying how many more are above or below.
func (m Model) fit(p popupBox, room, offset int) popupBox {
	if len(p.lines) <= room {
		return p
	}
	mid := max(room-p.head-p.tail, 1)
	body := p.lines[p.head : len(p.lines)-p.tail]
	offset = min(max(offset, 0), max(len(body)-mid, 0))
	shown := append([]string(nil), body[offset:min(offset+mid, len(body))]...)
	from, to := offset, offset+len(shown) // the body lines still shown whole
	if offset > 0 {
		shown[0] = m.more(m.g.up, offset+1, m.popupText())
		from++
	}
	if rest := len(body) - offset - len(shown); rest > 0 {
		shown[len(shown)-1] = m.more(m.g.down, rest+1, m.popupText())
		to--
	}
	out := popupBox{head: p.head, tail: p.tail}
	out.lines = append(append(append(out.lines, p.lines[:p.head]...), shown...), p.lines[len(p.lines)-p.tail:]...)
	for _, s := range p.spans {
		switch i := s.y - p.head; {
		case s.y < p.head:
		case s.y >= len(p.lines)-p.tail:
			s.y -= len(p.lines) - len(out.lines)
		case i >= from && i < to:
			s.y -= offset
		default:
			continue
		}
		out.spans = append(out.spans, s)
	}
	return out
}

// popupBox is the popup the screen shows, if any.
func (m Model) popupBox() (popupBox, bool) {
	switch {
	case m.confirmQuit:
		return m.quitBox(), true
	case m.dialog != nil:
		return m.dialogBox(), true
	case m.ask != nil:
		return m.dnsAskBox(), true
	case m.help:
		return m.helpBox(), true
	}
	return popupBox{}, false
}

// popupRoom is how many content lines a popup may take: the lines between
// the tab bar and the hint bar, less its border.
func (m Model) popupRoom() int { return max(m.height-4, 1) }

// placedPopup draws the popup centered between the tab bar and the hint bar
// and returns it with its screen position.
func (m Model) placedPopup(screen) (string, int, int, bool) {
	p, ok := m.popupBox()
	if !ok {
		return "", 0, 0, false
	}
	p = m.fit(p, m.popupRoom(), m.popupOffset())
	w := m.popupText()
	for i, l := range p.lines {
		l = ansi.Truncate(l, w, "")
		p.lines[i] = l + strings.Repeat(" ", max(w-lipgloss.Width(l), 0))
	}
	box := lipgloss.NewStyle().Border(m.g.border).BorderForeground(accent).Padding(0, 1).Render(strings.Join(p.lines, "\n"))
	x := max((m.width-lipgloss.Width(box))/2, 0)
	y := 1 + max((m.height-2-lipgloss.Height(box))/2, 0)
	return box, x, y, true
}

// popupSpans are the popup's clickable stretches on the screen, and its
// bounds: x0, y0, x1, y1.
func (m Model) popupSpans() ([]span, [4]int, bool) {
	box, x, y, ok := m.placedPopup(screen{})
	if !ok {
		return nil, [4]int{}, false
	}
	p, _ := m.popupBox()
	p = m.fit(p, m.popupRoom(), m.popupOffset())
	out := make([]span, len(p.spans))
	for i, s := range p.spans {
		out[i] = span{y + 1 + s.y, x + 2 + s.x0, x + 2 + s.x1, s.key}
	}
	return out, [4]int{x, y, x + lipgloss.Width(box), y + lipgloss.Height(box)}, true
}

// popupOffset is how far the popup is scrolled: only the help scrolls.
func (m Model) popupOffset() int {
	if m.confirmQuit || m.dialog != nil || m.ask != nil {
		return 0
	}
	return m.helpTop
}

// helpMax is how far the help scrolls.
func (m Model) helpMax() int {
	p := m.helpBox()
	if len(p.lines) <= m.popupRoom() {
		return 0
	}
	mid := max(m.popupRoom()-p.head-p.tail, 1)
	return max(len(p.lines)-p.head-p.tail-mid, 0)
}

// helpBox lists the page's keys, the keys that work everywhere, and the
// command that does each of the page's actions outside the TUI.
func (m Model) helpBox() popupBox {
	w := m.popupText()
	keyCol := lipgloss.NewStyle().Width(14)
	labelCol := lipgloss.NewStyle().Width(w - keyCol.GetWidth())
	var p popupBox
	line := func(k, label string) {
		p.lines = append(p.lines, strings.Split(lipgloss.JoinHorizontal(lipgloss.Top, keyCol.Render(keyStyle.Render(k)), labelCol.Render(label)), "\n")...)
	}
	title := i18n.T("disclaimer.title")
	if !m.showDisclaimer() {
		title = i18n.T("shell.help.page", pages[m.page].Title())
	}
	p.lines = []string{titleStyle.Render(title)}
	p.head = 1
	acts := m.actions()
	if a, ok := m.restartAction(); ok {
		acts = append([]action{a}, acts...)
	}
	for _, a := range acts {
		if a.Key != "" {
			line(keyName(a.Key), a.Label)
		}
	}
	p.lines = append(p.lines, "", groupStyle.Render(i18n.T("shell.help.global")))
	line(keyName("tab")+" / "+i18n.T("shell.help.shift_tab"), i18n.T("shell.help.pages"))
	line("1"+"–"+strconv.Itoa(len(pages)), i18n.T("shell.help.digits"))
	line(m.g.arrows+" j k", i18n.T("shell.help.arrows"))
	line(keyName("esc"), i18n.T("shell.help.back"))
	line(restartKey, i18n.T("shell.help.restart"))
	line(i18n.T("shell.help.click"), i18n.T("shell.help.click_does"))
	line("?", i18n.T("shell.help.toggle"))
	line("q", i18n.T("tui.act.quit"))
	var clis []string
	for _, a := range acts {
		if a.CLI != "" && a.Label != "" {
			clis = append(clis, mutedStyle.Render(a.Label+":")+" "+commandStyle.Render(a.CLI))
		}
	}
	if len(clis) > 0 {
		p.lines = append(p.lines, "", groupStyle.Render(i18n.T("shell.help.commands")))
		for _, c := range clis {
			p.lines = append(p.lines, strings.Split(ansi.Hardwrap(c, w, true), "\n")...)
		}
	}
	p.lines = append(p.lines, "")
	p.keys([2]string{m.g.arrows, i18n.T("shell.help.scroll")}, [2]string{"esc", i18n.T("shell.help.close")})
	p.tail = 2
	return p
}

// quitBox asks what becomes of the bypass this window runs when it quits.
func (m Model) quitBox() popupBox {
	var p popupBox
	p.lines = []string{titleStyle.Render(i18n.T("background_tui.quit_title"))}
	p.head = 1
	p.add(m, i18n.T("background_tui.quit"))
	p.lines = append(p.lines, "")
	move := i18n.T("background_tui.quit_background")
	if m.quitMove() == config.RunBoot {
		move = i18n.T("background_tui.quit_boot")
	}
	yes, no := i18n.T("confirm_keys.yes"), i18n.T("confirm_keys.no")
	p.choice(hint(yes, i18n.T("background_tui.quit_stop")), yes)
	p.choice(hint("b", move), "b")
	p.choice(hint(no, i18n.T("background_tui.quit_cancel")), no)
	p.tail = 3
	return p
}
