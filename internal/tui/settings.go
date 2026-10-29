package tui

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/KadirBerkpolat1/SplitDPI/internal/dns"
	"github.com/KadirBerkpolat1/SplitDPI/internal/i18n"
	"github.com/KadirBerkpolat1/SplitDPI/internal/ops"
)

// The settings page: one row per setting, each with its own actions.
type settingsPage struct{}

type settingsScreen struct {
	cursor      int
	dns         *dnsPicker // open while choosing the encrypted DNS
	result      []string   // what the last uninstall removed or left
	uninstalled bool
}

// dnsPicker chooses a DNS provider and protocol (`splitdpi dns`).
type dnsPicker struct {
	list   []ops.DNSProvider
	cursor int
	proto  dns.Protocol
	system string // the resolver that already encrypts the system's DNS, if any
}

type setting struct {
	label   func() string
	value   func(m Model) string
	actions func(m Model) []action
}

var settings = []setting{
	{label: func() string { return i18n.T("shell.settings.lang") }, value: langValue, actions: langActions},
	{label: func() string { return i18n.T("settings_dns.label") }, value: dnsValue, actions: dnsActions},
	{label: func() string { return i18n.T("settings_uninstall.label") }, value: uninstallValue, actions: uninstallActions},
}

// The languages offered; the others still work with --lang.
var langs = []string{"en", "tr"}

// The README there has the install command.
const repoURL = "github.com/KadirBerkpolat1/SplitDPI"

type (
	langMsg   struct{ err error }
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
	m.settings.dns = nil
	return nil
}

func (settingsPage) Actions(m Model) []action {
	switch {
	case m.settings.uninstalled:
		return []action{{Key: "esc", Label: i18n.T("tui.act.quit")}} // every key quits
	case m.settings.dns != nil:
		return m.dnsPickerActions()
	}
	return settings[m.settings.cursor].actions(m)
}

func (settingsPage) Key(m Model, _ tea.KeyMsg, s string) (Model, tea.Cmd, bool) {
	if m.settings.dns != nil {
		return m.dnsPickerKey(s)
	}
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
	switch msg := msg.(type) {
	case langMsg:
		m.note = i18n.T("shell.settings.lang_saved")
		if msg.err != nil {
			m.note = errText(msg.err)
		}
		return m, nil, true
	case dnsSetMsg:
		switch {
		case msg.err != nil:
			m.note = errText(msg.err)
			return m, nil, true
		case msg.off:
			m.note = i18n.T("dns.off")
		default:
			m.note = i18n.T("dns2.set", msg.name, msg.proto.Name())
		}
		if msg.running {
			m.note += " " + i18n.T("dns.restart")
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
			m.page, m.inPage, m.focus, m.note = pgSettings, true, 0, ""
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
	return m, nil, false
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
	}
	labelW := 0
	for _, st := range settings {
		labelW = max(labelW, lipgloss.Width(st.label()))
	}
	lines := make([]string, len(settings))
	for i, st := range settings {
		label := lipgloss.NewStyle().Width(labelW + 2).Render(st.label())
		lines[i] = m.listMark(i == s.cursor) + label + st.value(m)
	}
	out := scroll(lines, s.cursor, len(lines), w)
	if len(s.result) > 0 {
		res := lipgloss.NewStyle().Width(w).MaxHeight(max(h-len(out)-1, 1)).Render(strings.Join(s.result, "\n"))
		out = append(out, "", res)
	}
	return strings.Join(out, "\n")
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
	m.settings.dns, m.focus = p, 0
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
			m.settings.dns, m.focus = &p, 0
			return nil
		}},
		{Key: "esc", Label: i18n.T("tui.act.cancel"), Do: func(m *Model) tea.Cmd { m.settings.dns = nil; return nil }},
	}
}

// dnsPickerKey moves the cursor and leaves the action keys to the footer;
// other keys do nothing while the picker is open.
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
	case "enter", "t", "tab", "shift+tab", "left", "right":
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
		changes = append(changes, i18n.T("settings_dns.on_restart"))
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
	nameW := 0
	for _, d := range p.list {
		nameW = max(nameW, lipgloss.Width(d.Name))
	}
	blank := strings.Repeat(" ", lipgloss.Width(m.g.on))
	pre := lipgloss.Width(m.cursorMark(false)) + lipgloss.Width(blank) + 1 + nameW + 2
	note := lipgloss.NewStyle().Width(max(w-pre, 10))
	var lines []string
	at := 0
	for i, d := range p.list {
		mark := blank
		if d.ID == m.cfg.DNSProvider || d.ID == dns.Off && !m.cfg.DNSOn() {
			mark = goodStyle.Render(m.g.on)
		}
		if i == p.cursor {
			at = len(lines)
		}
		name := lipgloss.NewStyle().Width(nameW + 2).Render(d.Name)
		left := m.listMark(i == p.cursor) + mark + " " + name
		lines = append(lines, strings.Split(lipgloss.JoinHorizontal(lipgloss.Top, left, mutedStyle.Render(note.Render(d.Note))), "\n")...)
	}
	return strings.Join(append(head, scroll(lines, at, h-len(head), w)...), "\n")
}

func uninstallValue(Model) string { return mutedStyle.Render(i18n.T("settings_uninstall.value")) }

func uninstallActions(Model) []action {
	return []action{{Key: "enter", Label: i18n.T("settings_uninstall.do"), CLI: "sudo splitdpi uninstall --yes", Do: (*Model).confirmUninstall}}
}

// confirmUninstall lists what uninstall removes; nothing undoes it.
func (m *Model) confirmUninstall() tea.Cmd {
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

// uninstall stops the bypass this TUI runs, if any, then removes SplitDPI.
func (m *Model) uninstall() tea.Cmd {
	m.note = i18n.T("settings_uninstall.working")
	b, ctx, w := m.b, m.ctx, m.logs
	running, cancel, finished := m.running, m.runCancel, m.runFinished
	return func() tea.Msg {
		if running {
			cancel()
			<-finished
		}
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
