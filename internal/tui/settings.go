package tui

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/KadirBerkpolat1/SplitDPI/internal/config"
	"github.com/KadirBerkpolat1/SplitDPI/internal/dns"
	"github.com/KadirBerkpolat1/SplitDPI/internal/doctor"
	"github.com/KadirBerkpolat1/SplitDPI/internal/i18n"
	"github.com/KadirBerkpolat1/SplitDPI/internal/ops"
)

// The settings page: the four settings most people need, then the advanced
// ones and the maintenance actions. Enter on a row opens its editor or runs
// its action.
type settingsPage struct{ noClose }

type settingsScreen struct {
	cursor int

	// The editors; at most one is open.
	dns        *dnsPicker
	pick       *picker
	text       *textEditor
	ifaces     *ifaceEditor
	doctorOpen bool
	doctorTop  int // the first check the system check shows

	busy      string         // the maintenance row at work, if any
	checks    []doctor.Check // the last system check
	checking  bool
	engine    string   // the installed engine's version, "" if none
	presets   string   // the preset data's version
	takenOver []string // the services revert turns back on

	result      []string // what the last maintenance action did
	uninstalled bool
}

// dnsPicker chooses a DNS provider and protocol (`splitdpi dns`).
type dnsPicker struct {
	list   []ops.DNSProvider
	cursor int
	proto  dns.Protocol
	system string // the resolver that already encrypts the system's DNS, if any
}

// setting is one row. shown hides a row that does not apply here.
type setting struct {
	id      string
	group   int
	label   func() string
	value   func(m Model) string
	actions func(m Model) []action
	shown   func(m Model) bool
}

// The groups, in order.
const (
	grpGeneral = iota
	grpAdvanced
)

var groupKeys = []string{"general", "advanced"}

func groupName(g int) string { return i18n.T("settings_group." + groupKeys[g]) }

func label(key string) func() string { return func() string { return i18n.T(key) } }

var settings = []setting{
	{id: "lang", group: grpGeneral, label: label("shell.settings.lang"), value: langValue, actions: langActions},
	{id: "run_mode", group: grpGeneral, label: label("run_mode.label"), value: runModeValue, actions: runModeActions},
	{id: "dns", group: grpGeneral, label: label("settings_dns.label"), value: dnsValue, actions: dnsActions},
	{id: "mode", group: grpGeneral, label: label("settings_mode.label"), value: modeValue, actions: modeActions},
	{id: "skip_ifaces", group: grpAdvanced, label: label("settings_ifaces.label"), value: ifacesValue, actions: ifacesActions},
	{id: "qnum", group: grpAdvanced, label: label("settings_qnum.label"), value: qnumValue, actions: qnumActions},
	{id: "engine", group: grpAdvanced, label: label("settings_engine.label"), value: engineValue, actions: engineActions},
	{id: "presets", group: grpAdvanced, label: label("settings_presets.label"), value: presetsValue, actions: presetsActions},
	{id: "doctor", group: grpAdvanced, label: label("settings_doctor.label"), value: doctorValue, actions: doctorActions},
	{id: "takeover", group: grpAdvanced, label: label("settings_takeover.label"), value: takeoverValue, actions: takeoverActions, shown: canTakeOverAny},
	{id: "revert", group: grpAdvanced, label: label("settings_revert.label"), value: revertValue, actions: revertActions, shown: hasTakenOver},
	{id: "uninstall", group: grpAdvanced, label: label("settings_uninstall.label"), value: uninstallValue, actions: uninstallActions},
}

// The languages offered, as i18n supports them.
var langs = i18n.Supported()

// The README there has the install command.
const repoURL = "github.com/KadirBerkpolat1/SplitDPI"

type (
	langMsg    struct{ err error }
	runModeMsg struct {
		mode     config.RunMode
		disabled bool // the service was removed first
		err      error
	}
	dnsSetMsg struct {
		name    string
		proto   dns.Protocol
		off     bool
		running bool
		err     error
	}
	uninstallMsg struct {
		r   ops.UninstallReport
		err error
	}
)

func (settingsPage) Title() string { return i18n.T("shell.menu.settings") }
func (settingsPage) Hint() string  { return i18n.T("shell.hint.settings") }

func (settingsPage) Open(m *Model) tea.Cmd {
	s := &m.settings
	s.dns, s.pick, s.text, s.ifaces, s.doctorOpen = nil, nil, nil, nil, false
	m.loadSettings()
	return m.detectForeign()
}

// loadSettings reads what the rows show besides the config.
func (m *Model) loadSettings() {
	s := &m.settings
	s.engine = ""
	if e, ok := m.b.EngineStatus(); ok {
		s.engine = engineVersion(e)
	}
	s.presets = m.b.PresetsList().Version
	m.loadTakenOver()
}

// shownSettings are the rows that apply here.
func (m Model) shownSettings() []setting {
	var out []setting
	for _, st := range settings {
		if st.shown == nil || st.shown(m) {
			out = append(out, st)
		}
	}
	return out
}

// settingsCursor is the cursor kept within the n shown rows.
func (m Model) settingsCursor(n int) int { return max(min(m.settings.cursor, n-1), 0) }

// current is the row under the cursor, if any is shown.
func (m Model) current() (setting, bool) {
	rows := m.shownSettings()
	if len(rows) == 0 {
		return setting{}, false
	}
	return rows[m.settingsCursor(len(rows))], true
}

func (settingsPage) Actions(m Model) []action {
	s := m.settings
	switch {
	case s.uninstalled:
		return []action{{Key: "esc", Label: i18n.T("tui.act.quit")}} // every key quits
	case s.dns != nil:
		return m.dnsPickerActions()
	case s.pick != nil:
		return m.pickerActions()
	case s.text != nil:
		return m.textActions()
	case s.ifaces != nil:
		return m.ifaceActions()
	case s.doctorOpen:
		return m.doctorViewActions()
	}
	if st, ok := m.current(); ok {
		return st.actions(m)
	}
	return nil
}

func (settingsPage) Key(m Model, k tea.KeyMsg, s string) (Model, tea.Cmd, bool) {
	st := m.settings
	switch {
	case st.dns != nil:
		return m.dnsPickerKey(s)
	case st.pick != nil:
		return m.pickerKey(s)
	case st.text != nil:
		return m.textKey(k)
	case st.ifaces != nil:
		return m.ifaceKey(k)
	case st.doctorOpen:
		return m.doctorViewKey(s)
	}
	n := len(m.shownSettings())
	switch s {
	case "up", "k":
		m.settings.cursor = (m.settingsCursor(n) + n - 1) % max(n, 1)
		return m, nil, true
	case "down", "j":
		m.settings.cursor = (m.settingsCursor(n) + 1) % max(n, 1)
		return m, nil, true
	}
	return m, nil, false
}

func (settingsPage) Update(m Model, msg tea.Msg) (Model, tea.Cmd, bool) {
	switch msg := msg.(type) {
	case langMsg:
		m.note = i18n.T("shell.settings.lang_saved")
		if msg.err != nil {
			m.note = errText(msg.err)
		}
		return m, nil, true
	case runModeMsg:
		if msg.err != nil {
			m.note = errText(msg.err)
			return m, m.refresh(), true
		}
		m.cfg.RunMode = msg.mode
		m.note = i18n.T("run_mode.saved", runModeName(msg.mode))
		if msg.disabled {
			m.note = i18n.T("run_mode.disabled", runModeName(msg.mode))
		}
		return m, m.refresh(), true
	case dnsSetMsg:
		switch {
		case msg.err != nil:
			m.note = errText(msg.err)
			return m, nil, true
		case msg.off:
			m.note = i18n.T("tui.note.dns_off")
		default:
			m.note = i18n.T("tui.note.dns_on", msg.name, msg.proto.Name())
		}
		if msg.running {
			m.pending = true
			m.note += " " + i18n.T("tui.restart_hint", restartKey)
		}
		return m, m.refresh(), true
	case uninstallMsg:
		s := &m.settings
		s.result = nil
		for _, d := range msg.r.Removed {
			s.result = append(s.result, i18n.T("uninstall.removed", d))
		}
		for _, d := range msg.r.Restored {
			s.result = append(s.result, i18n.T("takeover.restored", d))
		}
		if msg.err == nil {
			s.uninstalled, s.dns, m.dialog, m.help = true, nil, nil, false
			m.page, m.note = pgSettings, ""
			return m, nil, true
		}
		for _, d := range msg.r.Leftover {
			s.result = append(s.result, warnStyle.Render(i18n.T("uninstall.leftover", d)))
		}
		m.note = errText(msg.err)
		if errors.Is(msg.err, ops.ErrUninstallIncomplete) {
			m.note = i18n.T("uninstall.not_clean")
		}
		return m, m.refresh(), true
	}
	if next, cmd, ok := m.editorUpdate(msg); ok {
		return next, cmd, true
	}
	return m.maintUpdate(msg)
}

func (settingsPage) View(m Model, w, h int) string {
	s := m.settings
	switch {
	case s.uninstalled:
		lines := append([]string{goodStyle.Render(i18n.T("uninstall.done")), ""}, s.result...)
		lines = append(lines, "", i18n.T("settings_uninstall.any_key"))
		return lipgloss.NewStyle().Width(w).MaxHeight(h).Render(strings.Join(lines, "\n"))
	case s.dns != nil:
		return m.dnsPickerView(w, h)
	case s.pick != nil:
		return m.pickerView(w, h)
	case s.text != nil:
		return m.textView(w, h)
	case s.ifaces != nil:
		return m.ifaceView(w, h)
	case s.doctorOpen:
		return m.doctorView(w, h)
	}
	return m.settingsList(w, h)
}

// settingsList is the rows under their group headers, then what the
// selected row is for and what the last action did.
func (m Model) settingsList(w, h int) string {
	s := m.settings
	labelW := 0
	for _, st := range settings {
		labelW = max(labelW, lipgloss.Width(st.label()))
	}
	labelW = min(labelW, w/2)
	rows := m.shownSettings()
	cur := m.settingsCursor(len(rows))
	var lines []string
	at, group := 0, -1
	for i, st := range rows {
		if st.group != group {
			if group >= 0 {
				lines = append(lines, "")
			}
			group = st.group
			lines = append(lines, groupStyle.Render(groupName(group)))
		}
		if i == cur {
			at = len(lines)
		}
		name := lipgloss.NewStyle().Width(labelW + 2).MaxWidth(labelW + 2).Render(st.label())
		lines = append(lines, m.listMark(i == cur)+name+st.value(m))
	}
	var tail []string
	if len(rows) > 0 {
		tail = append(tail, "")
		tail = append(tail, strings.Split(mutedStyle.Width(w).Render(i18n.T("settings_help."+rows[cur].id)), "\n")...)
	}
	if len(s.result) > 0 {
		tail = append(tail, "")
		tail = append(tail, strings.Split(lipgloss.NewStyle().Width(w).Render(strings.Join(s.result, "\n")), "\n")...)
	}
	// The rows keep at least a few lines; the tail gives way.
	room := max(h-len(tail), min(6, len(lines)))
	tail = tail[:min(len(tail), max(h-room, 0))]
	return strings.Join(append(scroll(lines, at, room, w), tail...), "\n")
}

// savedNote says a setting was saved as value, and when it applies.
func (m *Model) savedNote(label, value string) string {
	return m.withChange(i18n.T("settings_page.saved", label, value))
}

func langValue(Model) string { return i18n.T("shell.lang." + i18n.Lang()) }

// langActions: enter picks a language; it applies at once and is saved as
// the config's lang.
func langActions(Model) []action {
	return []action{{Key: "enter", Label: i18n.T("settings_page.lang_choose"), CLI: "sudo splitdpi config set lang " + i18n.Lang(), Do: func(m *Model) tea.Cmd { return m.openPicker("lang") }}}
}

func (m *Model) setLang(lang string) tea.Cmd {
	i18n.Init(lang)
	b := m.b
	return func() tea.Msg { return langMsg{b.SetLang(lang)} }
}

// The run modes in the picker's order: at boot first, this window last.
var runModes = []config.RunMode{config.RunBoot, config.RunBackground, config.RunWindow}

// runMode is the saved run mode; window while the config cannot be read,
// and in place of boot where the init has no service to start at boot.
func (m Model) runMode() config.RunMode {
	if m.cfg.RunMode == "" || m.cfg.RunMode == config.RunBoot && !m.svc.Supported {
		return config.RunWindow
	}
	return m.cfg.RunMode
}

func runModeName(mode config.RunMode) string { return i18n.T("run_mode." + string(mode)) }

// runModeCLI is the command that starts the bypass the way the status
// page's r does in mode.
func runModeCLI(mode config.RunMode) string {
	switch mode {
	case config.RunBackground:
		return "sudo splitdpi run --background"
	case config.RunBoot:
		return "sudo splitdpi enable"
	}
	return "sudo splitdpi run"
}

// runModeValue is the run mode, with a warning while boot lacks the service.
func runModeValue(m Model) string {
	if m.bootUnset() {
		return warnStyle.Render(i18n.T("boot.not_set_up"))
	}
	return runModeName(m.runMode())
}

// runModeActions set the missing service up; otherwise enter picks the mode.
func runModeActions(m Model) []action {
	if m.bootUnset() {
		return []action{{Key: "enter", Label: i18n.T("boot.set_up"), CLI: "sudo splitdpi enable", Do: (*Model).confirmEnable}}
	}
	return []action{{Key: "enter", Label: i18n.T("run_mode.change"), CLI: runModeCLI(m.runMode()), Do: func(m *Model) tea.Cmd {
		return m.openPicker("run_mode")
	}}}
}

// chooseRunMode applies the choice. Boot sets the service up at once, and
// leaving boot removes it, each after a confirm. Between window and
// background it only saves the choice: nothing starts or stops until r is
// pressed.
func (m *Model) chooseRunMode(mode config.RunMode) tea.Cmd {
	m.settings.pick = nil
	switch {
	case mode == config.RunBoot && !m.svc.Enabled:
		return m.confirmEnable()
	case mode != config.RunBoot && m.svc.Enabled:
		return m.confirmDisable(mode)
	}
	b := m.b
	return func() tea.Msg { return runModeMsg{mode: mode, err: b.SetRunMode(mode)} }
}

// runModeChoices are the run modes the picker offers: boot only where the
// init has a service.
func (m Model) runModeChoices() []config.RunMode {
	if m.svc.Supported {
		return runModes
	}
	return slices.DeleteFunc(slices.Clone(runModes), func(r config.RunMode) bool { return r == config.RunBoot })
}

// choiceList lays out a picker's choices, each name with its note wrapped
// beside it and the one in use marked. It returns the lines and the first
// line of the choice under the cursor.
func (m Model) choiceList(names, notes []string, inUse func(i int) bool, cursor, w int) ([]string, int) {
	nameW := 0
	for _, n := range names {
		nameW = max(nameW, lipgloss.Width(n))
	}
	nameW = min(nameW, w/2)
	blank := strings.Repeat(" ", lipgloss.Width(m.g.on))
	pre := lipgloss.Width(m.cursorMark(false)) + lipgloss.Width(blank) + 1 + nameW + 2
	note := lipgloss.NewStyle().Width(max(w-pre, 10))
	var lines []string
	at := 0
	for i, n := range names {
		mark := blank
		if inUse(i) {
			mark = goodStyle.Render(m.g.on)
		}
		if i == cursor {
			at = len(lines)
		}
		name := lipgloss.NewStyle().Width(nameW + 2).Render(n)
		left := m.listMark(i == cursor) + mark + " " + name
		lines = append(lines, strings.Split(lipgloss.JoinHorizontal(lipgloss.Top, left, mutedStyle.Render(note.Render(notes[i]))), "\n")...)
	}
	return lines, at
}

// dnsName is the shown name of a dns_provider id.
func (m Model) dnsName(id string) string {
	for _, p := range m.b.DNSProviders() {
		if p.ID == id {
			return p.Name
		}
	}
	return id
}

func dnsValue(m Model) string {
	if !m.cfg.DNSOn() {
		return mutedStyle.Render(i18n.T("dns2.off_name"))
	}
	return m.dnsName(m.cfg.DNSProvider) + mutedStyle.Render(" ("+m.cfg.DNSProtocol.Name()+")")
}

func dnsActions(Model) []action {
	return []action{{Key: "enter", Label: i18n.T("settings_dns.change"), CLI: "sudo splitdpi dns <id> [--dot]", Do: (*Model).openDNSPicker}}
}

func (m *Model) openDNSPicker() tea.Cmd {
	p := &dnsPicker{list: m.b.DNSProviders(), proto: m.cfg.DNSProtocol}
	if p.proto != dns.DoT {
		p.proto = dns.DoH
	}
	p.cursor = max(slices.IndexFunc(p.list, func(d ops.DNSProvider) bool { return d.ID == m.cfg.DNSProvider }), 0)
	p.system, _ = m.b.SystemEncryptedDNS(m.ctx)
	m.settings.dns = p
	return nil
}

// systemDNSNote says the system already encrypts DNS with name, and what
// SplitDPI's DNS still does then.
func systemDNSNote(name string) string {
	return i18n.T("dns_system.not_needed", name) + " " + i18n.T("dns_system.bypass_only")
}

// dnsCLI is the command that makes the same choice.
func dnsCLI(id string, proto dns.Protocol) string {
	cli := "sudo splitdpi dns " + id
	if proto == dns.DoT && id != dns.Off {
		cli += " --dot"
	}
	return cli
}

func (m Model) dnsPickerActions() []action {
	p := m.settings.dns
	other := dns.DoT
	if p.proto == dns.DoT {
		other = dns.DoH
	}
	id := p.list[p.cursor].ID
	return []action{
		{Key: "enter", Label: i18n.T("tui.act.choose"), CLI: dnsCLI(id, p.proto), Do: (*Model).chooseDNS},
		{Key: "t", Label: i18n.T("settings_dns.use", other.Name()), CLI: dnsCLI(id, other), Do: func(m *Model) tea.Cmd {
			p := *m.settings.dns
			p.proto = other
			m.settings.dns = &p
			return nil
		}},
		{Key: "esc", Label: i18n.T("tui.act.cancel"), Do: func(m *Model) tea.Cmd { m.settings.dns = nil; return nil }},
	}
}

// dnsPickerKey moves the cursor and leaves the action keys to the hint bar.
func (m Model) dnsPickerKey(s string) (Model, tea.Cmd, bool) {
	p := *m.settings.dns
	n := len(p.list)
	switch s {
	case "up", "k":
		p.cursor = (p.cursor + n - 1) % n
	case "down", "j":
		p.cursor = (p.cursor + 1) % n
	case "esc":
		m.settings.dns = nil
		return m, nil, true
	default:
		return m, nil, false
	}
	m.settings.dns = &p
	return m, nil, true
}

// chooseDNS asks before encrypted DNS is turned on; turning it off removes
// a change, so it is done at once.
func (m *Model) chooseDNS() tea.Cmd {
	p := m.settings.dns
	ch, proto := p.list[p.cursor], p.proto
	if ch.ID == dns.Off {
		return m.setDNS(ch, dns.DoH)
	}
	changes := []string{i18n.T("settings_dns.via", ch.Name, proto.Name())}
	if ch.Note != "" {
		changes = append(changes, ch.Note)
	}
	if p.system != "" {
		changes = append(changes, systemDNSNote(p.system))
	}
	if m.bypassOn() {
		changes = append(changes, i18n.T("settings_dns.on_restart", restartKey))
	}
	m.dialog = &confirm{
		title:   i18n.T("settings_dns.title"),
		changes: changes,
		undo:    "sudo splitdpi dns off",
		yes:     func(m *Model) tea.Cmd { return m.setDNS(ch, proto) },
	}
	return nil
}

func (m *Model) setDNS(ch ops.DNSProvider, proto dns.Protocol) tea.Cmd {
	m.settings.dns = nil
	b, w := m.b, m.logs
	return func() tea.Msg {
		running, err := b.SetDNS(ch.ID, proto)
		if err != nil {
			fmt.Fprintln(w, err)
		}
		return dnsSetMsg{ch.Name, proto, ch.ID == dns.Off, running, err}
	}
}

// dnsPickerView lists the providers with what each does, under the protocol.
func (m Model) dnsPickerView(w, h int) string {
	p := m.settings.dns
	head := []string{i18n.T("settings_dns.protocol") + " " + titleStyle.Render(p.proto.Name())}
	if p.system != "" {
		head = append(head, strings.Split(lipgloss.NewStyle().Width(w).Render(goodStyle.Render(systemDNSNote(p.system))), "\n")...)
	}
	if p.proto == dns.DoT {
		head = append(head, strings.Split(lipgloss.NewStyle().Width(w).Render(warnStyle.Render(i18n.T("settings_dns.dot_note"))), "\n")...)
	}
	head = append(head, "")
	names, notes := make([]string, len(p.list)), make([]string, len(p.list))
	for i, d := range p.list {
		names[i], notes[i] = d.Name, d.Note
	}
	lines, at := m.choiceList(names, notes, func(i int) bool {
		id := p.list[i].ID
		return id == m.cfg.DNSProvider || id == dns.Off && !m.cfg.DNSOn()
	}, p.cursor, w)
	return strings.Join(append(head, scroll(lines, at, h-len(head), w)...), "\n")
}

func uninstallValue(Model) string { return mutedStyle.Render(i18n.T("settings_uninstall.value")) }

func uninstallActions(Model) []action {
	return []action{{Key: "enter", Label: i18n.T("settings_uninstall.do"), CLI: "sudo splitdpi uninstall --yes", Do: (*Model).confirmUninstall}}
}

// confirmUninstall lists what uninstall removes; nothing undoes it.
func (m *Model) confirmUninstall() tea.Cmd {
	if m.waitHeld() {
		return nil
	}
	p := m.b.UninstallPlan()
	if p.Service == "" && len(p.Paths) == 0 {
		m.note = i18n.T("uninstall.nothing")
		return nil
	}
	changes := []string{i18n.T("settings_uninstall.stop")}
	if p.Service != "" {
		changes = append(changes, i18n.T("settings_uninstall.service", p.Service))
	}
	if len(p.Paths) > 0 {
		changes = append(changes, i18n.T("settings_uninstall.paths", strings.Join(p.Paths, ", ")))
	}
	changes = append(changes, i18n.T("settings_uninstall.restore"))
	m.dialog = &confirm{
		title:    i18n.T("settings_uninstall.title"),
		changes:  changes,
		undoHead: i18n.T("settings_uninstall.no_undo"),
		undo:     repoURL,
		yes:      (*Model).uninstall,
	}
	return nil
}

// stopOwnRun returns a func that stops the bypass this TUI runs, if any,
// and waits until its rules are gone; it runs inside a command.
func (m *Model) stopOwnRun() func() {
	running, cancel, j := m.running, m.runCancel, m.runJob
	return func() {
		if running {
			cancel()
			j.wait()
		}
	}
}

// uninstall stops the bypass this TUI runs, if any, then removes SplitDPI.
func (m *Model) uninstall() tea.Cmd {
	m.note = i18n.T("settings_uninstall.working")
	b, ctx, w, stop := m.b, m.ctx, m.logs, m.stopOwnRun()
	return func() tea.Msg {
		stop()
		r, err := b.Uninstall(ctx)
		for _, d := range r.Leftover {
			fmt.Fprintln(w, i18n.T("uninstall.leftover", d))
		}
		if err != nil {
			fmt.Fprintln(w, err)
		}
		return uninstallMsg{r, err}
	}
}
