package tui

import (
	"errors"
	"slices"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/KadirBerkpolat1/SplitDPI/internal/config"
	"github.com/KadirBerkpolat1/SplitDPI/internal/i18n"
	"github.com/KadirBerkpolat1/SplitDPI/internal/ops"
)

// The settings editors: a picker for each setting with a few fixed values,
// a text input for one value, and a list editor for skip_ifaces. Each saves
// through ops and shows a refusal inside the editor.

// picker chooses one of a setting's values; err is the last refusal.
type picker struct {
	id     string
	cursor int
	err    string
}

// choices are what a picker offers: each name with a one-line note, the one
// in use, the command that makes each choice, and what choosing does.
type choices struct {
	head         string
	names, notes []string
	inUse        int
	cli          func(i int) string
	choose       func(m *Model, i int) tea.Cmd
}

// The scopes in the picker's order.
var modes = []config.Mode{config.ModeBroad, config.ModeApps}

func (m Model) choicesFor(id string) choices {
	switch id {
	case "lang":
		c := choices{head: i18n.T("settings_page.lang_head"), inUse: slices.Index(langs, i18n.Lang())}
		for _, l := range langs {
			c.names, c.notes = append(c.names, i18n.T("shell.lang."+l)), append(c.notes, "")
		}
		c.cli = func(i int) string { return "sudo splitdpi config set lang " + langs[i] }
		c.choose = func(m *Model, i int) tea.Cmd {
			m.settings.pick = nil
			return m.setLang(langs[i])
		}
		return c
	case "run_mode":
		c := choices{head: i18n.T("run_mode.head"), inUse: slices.Index(runModes, m.runMode())}
		for _, r := range runModes {
			c.names, c.notes = append(c.names, runModeName(r)), append(c.notes, i18n.T("run_mode."+string(r)+"_note"))
		}
		c.cli = func(i int) string { return runModeCLI(runModes[i]) }
		c.choose = func(m *Model, i int) tea.Cmd { return m.chooseRunMode(runModes[i]) }
		return c
	}
	c := choices{head: i18n.T("settings_mode.head"), inUse: slices.Index(modes, m.mode())}
	for _, md := range modes {
		c.names, c.notes = append(c.names, modeName(md)), append(c.notes, i18n.T("settings_mode."+string(md)+"_note"))
	}
	c.cli = func(i int) string { return "sudo splitdpi config set mode " + string(modes[i]) }
	c.choose = func(m *Model, i int) tea.Cmd {
		mode, b := modes[i], m.b
		return func() tea.Msg { return modeMsg{mode, b.SetMode(mode)} }
	}
	return c
}

// openPicker opens id's picker on the value in use.
func (m *Model) openPicker(id string) tea.Cmd {
	m.settings.pick, m.focus = &picker{id: id, cursor: max(m.choicesFor(id).inUse, 0)}, 0
	return nil
}

func (m Model) pickerActions() []action {
	p := m.settings.pick
	c := m.choicesFor(p.id)
	return []action{
		{Key: "enter", Label: i18n.T("tui.act.choose"), CLI: c.cli(p.cursor), Do: func(m *Model) tea.Cmd { return c.choose(m, m.settings.pick.cursor) }},
		{Key: "esc", Label: i18n.T("tui.act.cancel"), Do: func(m *Model) tea.Cmd { m.settings.pick = nil; return nil }},
	}
}

// pickerKey moves the cursor and leaves enter to the footer; other keys do
// nothing while the picker is open.
func (m Model) pickerKey(s string) (Model, tea.Cmd, bool) {
	p := *m.settings.pick
	n := len(m.choicesFor(p.id).names)
	switch s {
	case "up", "k":
		p.cursor = (p.cursor + n - 1) % n
	case "down", "j":
		p.cursor = (p.cursor + 1) % n
	case "esc":
		m.settings.pick = nil
		return m, nil, true
	case "enter", "tab", "shift+tab", "left", "right":
		return m, nil, false
	}
	m.settings.pick = &p
	return m, nil, true
}

func (m Model) pickerView(w, h int) string {
	p := m.settings.pick
	c := m.choicesFor(p.id)
	head := append(strings.Split(lipgloss.NewStyle().Width(w).Render(c.head), "\n"), "")
	lines, at := m.choiceList(c.names, c.notes, func(i int) bool { return i == c.inUse }, p.cursor, w)
	var tail []string
	if p.err != "" {
		tail = append([]string{""}, strings.Split(badStyle.Width(w).Render(p.err), "\n")...)
	}
	out := append(head, scroll(lines, at, h-len(head)-len(tail), w)...)
	return strings.Join(append(out, tail...), "\n")
}

// mode is the saved scope; broad while the config cannot be read.
func (m Model) mode() config.Mode {
	if m.cfg.Mode == "" {
		return config.ModeBroad
	}
	return m.cfg.Mode
}

func modeName(md config.Mode) string { return i18n.T("settings_mode." + string(md)) }

func modeValue(m Model) string { return modeName(m.mode()) }

func modeActions(m Model) []action {
	return []action{{Key: "enter", Label: i18n.T("settings_mode.change"), CLI: "sudo splitdpi config set mode " + string(m.mode()), Do: func(m *Model) tea.Cmd {
		return m.openPicker("mode")
	}}}
}

func excludeValue(m Model) string {
	if m.settings.excluded == 0 {
		return mutedStyle.Render(i18n.T("tui.none"))
	}
	return i18n.N("settings_exclude.count", m.settings.excluded)
}

// excludeActions jump to the Sites page, where the exclusions close its list.
func excludeActions(Model) []action {
	return []action{{Key: "enter", Label: i18n.T("settings_exclude.go"), CLI: "sudo splitdpi exclude list", Do: func(m *Model) tea.Cmd {
		cmd := m.jumpTo(pgSites)
		m.apps.cursor = max(m.apps.rows()-1, 0)
		m.note = i18n.T("settings_exclude.jumped")
		return cmd
	}}}
}

func strategyValue(m Model) string {
	if s := m.cfg.Strategy; s != nil {
		return s.ID
	}
	return warnStyle.Render(i18n.T("tui.strategy_none"))
}

func strategyActions(Model) []action {
	return []action{{Key: "enter", Label: i18n.T("settings_strategy.go"), CLI: "sudo splitdpi strategy list", Do: func(m *Model) tea.Cmd {
		return m.jumpTo(pgStrategy)
	}}}
}

// jumpTo opens page pg with the keys in it.
func (m *Model) jumpTo(pg int) tea.Cmd {
	m.page, m.inPage, m.focus, m.note = pg, true, 0, ""
	return pages[pg].Open(m)
}

// textEditor edits one value typed in full: ipinfo_url or qnum.
type textEditor struct {
	id    string
	input textinput.Model
	err   string
}

func ipinfoValue(m Model) string {
	if m.cfg.IPInfoURL == "" {
		return config.Default().IPInfoURL
	}
	return m.cfg.IPInfoURL
}

func ipinfoActions(m Model) []action {
	return []action{{Key: "enter", Label: i18n.T("settings_page.edit"), CLI: "sudo splitdpi config set ipinfo_url " + shellArg(ipinfoValue(m)), Do: func(m *Model) tea.Cmd {
		return m.openText("ipinfo_url", ipinfoValue(*m), 2048)
	}}}
}

func qnumValue(m Model) string { return strconv.Itoa(m.cfg.QNum) }

func qnumActions(m Model) []action {
	return []action{{Key: "enter", Label: i18n.T("settings_page.edit"), CLI: "sudo splitdpi config set qnum " + qnumValue(m), Do: func(m *Model) tea.Cmd {
		return m.openText("qnum", qnumValue(*m), 5)
	}}}
}

func (m *Model) openText(id, value string, limit int) tea.Cmd {
	in := textinput.New()
	in.CharLimit, in.Width = limit, 48
	in.SetValue(value)
	cmd := in.Focus()
	m.settings.text, m.focus = &textEditor{id: id, input: in}, 0
	return cmd
}

// textCLI is the command that saves what is typed.
func (t textEditor) cli() string {
	v := strings.TrimSpace(t.input.Value())
	if v == "" && t.id == "ipinfo_url" {
		return "sudo splitdpi config reset ipinfo_url"
	}
	return "sudo splitdpi config set " + t.id + " " + shellArg(v)
}

func (m Model) textActions() []action {
	return []action{
		{Key: "enter", Label: i18n.T("settings_page.save"), CLI: m.settings.text.cli(), Do: (*Model).saveText},
		{Key: "esc", Label: i18n.T("tui.act.cancel"), Do: func(m *Model) tea.Cmd { m.settings.text = nil; return nil }},
	}
}

// textKey feeds the input; enter is the footer's save.
func (m Model) textKey(k tea.KeyMsg) (Model, tea.Cmd, bool) {
	switch k.String() {
	case "enter":
		return m, nil, false
	case "esc":
		m.settings.text = nil
		return m, nil, true
	}
	t := *m.settings.text
	var cmd tea.Cmd
	t.input, cmd = t.input.Update(k)
	t.err = ""
	m.settings.text = &t
	return m, cmd, true
}

type textSavedMsg struct {
	id, value string
	err       error
}

// saveText saves the typed value; ops checks it, except that qnum must be
// a number to be passed at all.
func (m *Model) saveText() tea.Cmd {
	t := *m.settings.text
	v, b := strings.TrimSpace(t.input.Value()), m.b
	if t.id == "qnum" {
		n, err := strconv.Atoi(v)
		if err != nil {
			t.err = i18n.T("settings_qnum.not_number")
			m.settings.text = &t
			return nil
		}
		return func() tea.Msg { return textSavedMsg{t.id, strconv.Itoa(n), b.SetQNum(n)} }
	}
	return func() tea.Msg { return textSavedMsg{t.id, v, b.SetIPInfoURL(v)} }
}

func (m Model) textView(w, h int) string {
	t := m.settings.text
	block := func(style lipgloss.Style, s string) []string { return strings.Split(style.Width(w).Render(s), "\n") }
	var lines []string
	switch t.id {
	case "qnum":
		lines = append(block(lipgloss.NewStyle(), i18n.T("settings_qnum.head")), "")
		lines = append(lines, block(warnStyle, i18n.T("settings_qnum.warn"))...)
	default:
		lines = block(lipgloss.NewStyle(), i18n.T("settings_ipinfo.head"))
	}
	lines = append(lines, "", ansi.Truncate(t.input.View(), w, ""))
	if t.err != "" {
		lines = append(lines, block(badStyle, t.err)...)
	}
	hint := i18n.T("settings_qnum.default", config.Default().QNum)
	if t.id == "ipinfo_url" {
		hint = i18n.T("settings_ipinfo.default", config.Default().IPInfoURL)
	}
	lines = append(lines, "")
	lines = append(lines, block(mutedStyle, hint)...)
	return lipgloss.NewStyle().MaxHeight(h).Render(strings.Join(lines, "\n"))
}

// ifaceEditor adds and removes the interfaces the bypass leaves alone.
type ifaceEditor struct {
	list   []string
	cursor int
	adding bool
	input  textinput.Model
	err    string
}

func ifacesValue(m Model) string {
	if len(m.cfg.SkipIfaces) == 0 {
		return mutedStyle.Render(i18n.T("tui.none"))
	}
	return strings.Join(m.cfg.SkipIfaces, ", ")
}

func ifacesCLI(list []string) string {
	return "sudo splitdpi config set skip_ifaces " + shellArg(strings.Join(list, ","))
}

// shellArg quotes s for a shell when it is empty or holds anything but
// plain characters, so that a pasted command passes it unchanged.
func shellArg(s string) string {
	plain := s != "" && !strings.ContainsFunc(s, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("._/:@%+=,-", r))
	})
	if plain {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func ifacesActions(m Model) []action {
	return []action{{Key: "enter", Label: i18n.T("settings_page.edit"), CLI: ifacesCLI(m.cfg.SkipIfaces), Do: func(m *Model) tea.Cmd {
		m.settings.ifaces, m.focus = &ifaceEditor{list: slices.Clone(m.cfg.SkipIfaces)}, 0
		return nil
	}}}
}

func (m Model) ifaceActions() []action {
	e := m.settings.ifaces
	if e.adding {
		typed := strings.TrimSpace(e.input.Value())
		return []action{
			{Key: "enter", Label: i18n.T("settings_ifaces.add"), CLI: ifacesCLI(append(slices.Clone(e.list), typed)), Do: (*Model).addIface},
			{Key: "esc", Label: i18n.T("tui.act.cancel"), Do: func(m *Model) tea.Cmd { return nil }}, // ifaceKey cancels
		}
	}
	acts := []action{{Key: "a", Label: i18n.T("settings_ifaces.add"), CLI: ifacesCLI(append(slices.Clone(e.list), "NAME")), Do: func(m *Model) tea.Cmd {
		e := *m.settings.ifaces
		e.adding, e.err, e.input = true, "", textinput.New()
		e.input.Placeholder, e.input.CharLimit = "wg*", 16
		cmd := e.input.Focus()
		m.settings.ifaces, m.focus = &e, 0
		return cmd
	}}}
	if len(e.list) > 0 {
		rest := slices.Delete(slices.Clone(e.list), e.cursor, e.cursor+1)
		acts = append(acts, action{Key: "x", Label: i18n.T("tui.act.remove"), CLI: ifacesCLI(rest), Do: func(m *Model) tea.Cmd {
			return m.saveIfaces(rest)
		}})
	}
	if def := config.Default().SkipIfaces; !slices.Equal(e.list, def) {
		acts = append(acts, action{Key: "d", Label: i18n.T("settings_ifaces.default"), CLI: "sudo splitdpi config reset skip_ifaces", Do: func(m *Model) tea.Cmd {
			return m.saveIfaces(def)
		}})
	}
	return append(acts, action{Key: "esc", Label: i18n.T("settings_ifaces.close"), Do: func(m *Model) tea.Cmd { m.settings.ifaces = nil; return nil }})
}

// ifaceKey feeds the input while adding and moves the cursor over the list.
func (m Model) ifaceKey(k tea.KeyMsg) (Model, tea.Cmd, bool) {
	e := *m.settings.ifaces
	if e.adding {
		switch k.String() {
		case "enter":
			return m, nil, false
		case "esc":
			e.adding, e.err = false, ""
			m.settings.ifaces = &e
			return m, nil, true
		}
		var cmd tea.Cmd
		e.input, cmd = e.input.Update(k)
		e.err = ""
		m.settings.ifaces = &e
		return m, cmd, true
	}
	n := len(e.list)
	switch k.String() {
	case "up", "k":
		if n > 0 {
			e.cursor = (e.cursor + n - 1) % n
		}
	case "down", "j":
		if n > 0 {
			e.cursor = (e.cursor + 1) % n
		}
	case "esc":
		m.settings.ifaces = nil
		return m, nil, true
	default:
		return m, nil, false
	}
	m.settings.ifaces = &e
	return m, nil, true
}

func (m *Model) addIface() tea.Cmd {
	e := m.settings.ifaces
	typed := strings.TrimSpace(e.input.Value())
	if typed == "" {
		return nil
	}
	return m.saveIfaces(append(slices.Clone(e.list), typed))
}

type ifacesMsg struct {
	list []string
	err  error
}

func (m *Model) saveIfaces(list []string) tea.Cmd {
	b := m.b
	return func() tea.Msg { return ifacesMsg{list, b.SetSkipIfaces(list)} }
}

func (m Model) ifaceView(w, h int) string {
	e := m.settings.ifaces
	head := append(strings.Split(lipgloss.NewStyle().Width(w).Render(i18n.T("settings_ifaces.head")), "\n"), "")
	lines := make([]string, len(e.list))
	for i, name := range e.list {
		lines[i] = m.listMark(i == e.cursor && !e.adding) + name
	}
	if len(lines) == 0 {
		lines = []string{mutedStyle.Render(i18n.T("settings_ifaces.empty"))}
	}
	var tail []string
	if e.adding {
		tail = append(tail, "")
		tail = append(tail, strings.Split(lipgloss.NewStyle().Width(w).Render(i18n.T("settings_ifaces.which")), "\n")...)
		tail = append(tail, ansi.Truncate(e.input.View(), w, ""))
	}
	if e.err != "" {
		tail = append(tail, "")
		tail = append(tail, strings.Split(badStyle.Width(w).Render(e.err), "\n")...)
	}
	out := append(head, scroll(lines, e.cursor, h-len(head)-len(tail), w)...)
	return strings.Join(append(out, tail...), "\n")
}

type modeMsg struct {
	mode config.Mode
	err  error
}

// editorUpdate takes the editors' results: a refusal stays in the open
// editor, a saved value closes it.
func (m Model) editorUpdate(msg tea.Msg) (Model, tea.Cmd, bool) {
	s := &m.settings
	switch msg := msg.(type) {
	case modeMsg:
		if msg.err != nil {
			text := errText(msg.err)
			if errors.Is(msg.err, ops.ErrAppsModeEmpty) {
				text = i18n.T("settings_mode.empty")
			}
			if s.pick != nil && s.pick.id == "mode" {
				p := *s.pick
				p.err = text
				s.pick = &p
			} else {
				m.note = text
			}
			return m, nil, true
		}
		s.pick, m.cfg.Mode = nil, msg.mode
		m.note = m.savedNote(i18n.T("settings_mode.label"), modeName(msg.mode))
		return m, m.refresh(), true
	case textSavedMsg:
		if msg.err != nil {
			if s.text != nil && s.text.id == msg.id {
				t := *s.text
				t.err = errText(msg.err)
				s.text = &t
			} else {
				m.note = errText(msg.err)
			}
			return m, nil, true
		}
		s.text = nil
		if msg.id == "qnum" {
			m.cfg.QNum, _ = strconv.Atoi(msg.value)
			m.note = m.savedNote(i18n.T("settings_qnum.label"), msg.value)
		} else {
			m.cfg.IPInfoURL = msg.value
			m.note = m.savedNote(i18n.T("settings_ipinfo.label"), ipinfoValue(m))
		}
		return m, m.refresh(), true
	case ifacesMsg:
		if msg.err != nil {
			if s.ifaces != nil {
				e := *s.ifaces
				e.err = errText(msg.err)
				s.ifaces = &e
			} else {
				m.note = errText(msg.err)
			}
			return m, nil, true
		}
		m.cfg.SkipIfaces = msg.list
		if s.ifaces != nil {
			e := *s.ifaces
			e.list, e.adding, e.err = slices.Clone(msg.list), false, ""
			e.cursor = max(min(e.cursor, len(e.list)-1), 0)
			s.ifaces = &e
		}
		m.note = m.savedNote(i18n.T("settings_ifaces.label"), ansi.Strip(ifacesValue(m)))
		return m, m.refresh(), true
	}
	return m, nil, false
}
