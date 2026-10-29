package tui

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/KadirBerkpolat1/SplitDPI/internal/doctor"
	"github.com/KadirBerkpolat1/SplitDPI/internal/domain"
	"github.com/KadirBerkpolat1/SplitDPI/internal/i18n"
	"github.com/KadirBerkpolat1/SplitDPI/internal/presets"
	"github.com/KadirBerkpolat1/SplitDPI/internal/scan"
)

// The scan page is the setup wizard:
// doctor → detect → apps → [one site] → scan → run or enable.
type scanPage struct{}

type wizStep int

const (
	wIntro wizStep = iota // not started
	wDoctor
	wLookup
	wPick
	wApps
	wSite
	wScan
	wDone
)

type wizard struct {
	step   wizStep
	checks []doctor.Check
	host   string
	info   scan.ASNInfo
	cursor int // picker row or app row
	apps   []presets.App
	chosen map[string]bool

	// A site scan: blockcheck2 on one domain instead of the preset trials.
	input textinput.Model
	site  string // normalized; empty for the preset trials
	ipv6  bool

	progress   *ring
	scanCancel context.CancelFunc
	scanning   bool
	cancelled  bool
	report     *scan.Report
	err        error
}

type (
	doctorMsg struct{ checks []doctor.Check }
	detectMsg struct {
		info scan.ASNInfo
		err  error
	}
	appsAppliedMsg struct{ err error }
	scanDoneMsg    struct {
		rep scan.Report
		err error
	}
	scanTickMsg struct{}
	enableMsg   struct{ err error }
)

func (scanPage) Title() string                                              { return i18n.T("shell.menu.scan") }
func (scanPage) Hint() string                                               { return i18n.T("shell.hint.scan") }
func (scanPage) Open(*Model) tea.Cmd                                        { return nil }
func (scanPage) Actions(m Model) []action                                   { return m.wizardActions() }
func (scanPage) Update(m Model, msg tea.Msg) (Model, tea.Cmd, bool)         { return m.wizardUpdate(msg) }
func (scanPage) Key(m Model, k tea.KeyMsg, s string) (Model, tea.Cmd, bool) { return m.wizardKey(k, s) }
func (scanPage) View(m Model, w, h int) string                              { return m.wizardView(w, h) }

func (m *Model) openWizard() tea.Cmd {
	m.focus, m.note = 0, ""
	m.wiz = wizard{step: wDoctor, progress: &ring{max: 200}}
	return m.runDoctor()
}

// finishWizard leaves the finished wizard for the status page.
func (m *Model) finishWizard() {
	m.wiz = wizard{}
	m.page, m.inPage, m.focus = pgStatus, false, 0
}

func (m *Model) runDoctor() tea.Cmd {
	b, ctx := m.b, m.ctx
	m.note = i18n.T("tui.wiz.checking")
	return func() tea.Msg { return doctorMsg{b.Doctor(ctx)} }
}

func blockers(cs []doctor.Check) []doctor.Check {
	var out []doctor.Check
	for _, c := range cs {
		if c.Severity == doctor.Blocker {
			out = append(out, c)
		}
	}
	return out
}

func (m Model) wizardUpdate(msg tea.Msg) (Model, tea.Cmd, bool) {
	w := &m.wiz
	switch msg := msg.(type) {
	case doctorMsg:
		w.checks, m.note = msg.checks, ""
		if len(blockers(msg.checks)) == 0 {
			w.step, m.focus = wLookup, 0
			host, err := m.b.IPInfoHost()
			if err != nil {
				m.note = errText(err)
			}
			w.host = host
		}
		return m, nil, true
	case detectMsg:
		m.note = ""
		if msg.err != nil {
			m.note = errText(msg.err)
			w.step, w.cursor, m.focus = wPick, 0, 0
			return m, nil, true
		}
		w.info = msg.info
		m.chooseApps()
		return m, nil, true
	case appsAppliedMsg:
		if msg.err != nil {
			m.note = errText(msg.err)
			return m, nil, true
		}
		m, cmd := m.withCmd((*Model).startScan)
		return m, cmd, true
	case scanTickMsg:
		if w.scanning {
			return m, m.every(300*time.Millisecond, func(time.Time) tea.Msg { return scanTickMsg{} }), true
		}
		return m, nil, true
	case scanDoneMsg:
		w.scanning = false
		switch {
		case w.cancelled:
		case msg.err != nil:
			w.err = msg.err
			var bc scan.BlockcheckError
			if errors.As(msg.err, &bc) {
				for _, l := range bc.Tail {
					w.progress.Write([]byte(l + "\n"))
				}
			}
		case msg.rep.Winner == nil:
			w.err = scan.ErrNoStrategy
		default:
			w.report = &msg.rep
			w.step, m.focus = wDone, 0
			if w.site != "" {
				m.note = i18n.T("sites.added", w.site)
			}
		}
		return m, m.refresh(), true
	case enableMsg:
		if msg.err != nil {
			m.note = errText(msg.err)
			return m, nil, true
		}
		m.finishWizard()
		m.note = i18n.T("service.enabled")
		return m, m.refresh(), true
	}
	return m, nil, false
}

func (m Model) withCmd(f func(*Model) tea.Cmd) (Model, tea.Cmd) {
	cmd := f(&m)
	return m, cmd
}

// chooseApps moves to the apps step, preselecting the configured apps or
// else the detected country's defaults.
func (m *Model) chooseApps() {
	w := &m.wiz
	set := m.b.PresetsList()
	w.apps = w.apps[:0]
	for _, id := range slices.Sorted(maps.Keys(set.Apps)) {
		w.apps = append(w.apps, set.Apps[id])
	}
	pre := m.cfg.Apps
	if len(pre) == 0 {
		pre = set.Countries[strings.ToUpper(w.info.Country)].DefaultApps
	}
	w.chosen = map[string]bool{}
	for _, id := range pre {
		w.chosen[id] = true
	}
	w.step, w.cursor, m.focus = wApps, 0, 0
}

func (w wizard) chosenList() []string {
	var ids []string
	for _, a := range w.apps {
		if w.chosen[a.ID] {
			ids = append(ids, a.ID)
		}
	}
	return ids
}

// scanCLI is the command that runs the same scan.
func (w wizard) scanCLI() string {
	parts := []string{"sudo splitdpi scan"}
	if w.info.ASN != 0 {
		parts = append(parts, "--asn "+strconv.Itoa(w.info.ASN))
	}
	if w.info.Country != "" {
		parts = append(parts, "--country "+w.info.Country)
	}
	if w.site != "" {
		parts = append(parts, "--site "+w.site)
	} else if ids := w.chosenList(); len(ids) > 0 {
		parts = append(parts, "--apps "+strings.Join(ids, ","))
	}
	return strings.Join(parts, " ")
}

func (m Model) pickRows() []presets.ISP { return m.b.PresetsList().ISPs }

func (m Model) wizardActions() []action {
	w := m.wiz
	switch w.step {
	case wIntro:
		return []action{{Key: "w", Label: i18n.T("shell.scan.start"), CLI: "sudo splitdpi scan", Do: (*Model).openWizard}}
	case wDoctor:
		return []action{{Key: "enter", Label: i18n.T("tui.wiz.check_again"), CLI: "sudo splitdpi doctor", Do: (*Model).runDoctor}}
	case wLookup:
		return []action{
			{Key: "y", Label: i18n.T("tui.wiz.look_up"), CLI: "sudo splitdpi detect", Do: (*Model).detect},
			{Key: "n", Label: i18n.T("tui.wiz.pick_isp"), Do: func(m *Model) tea.Cmd { m.wiz.step, m.wiz.cursor, m.focus = wPick, 0, 0; return nil }},
		}
	case wPick:
		cli := ""
		if rows := m.pickRows(); w.cursor < len(rows) && len(rows[w.cursor].ASN) > 0 {
			isp := rows[w.cursor]
			cli = fmt.Sprintf("sudo splitdpi detect --asn %d --country %s", isp.ASN[0], isp.Country)
		}
		return []action{{Key: "enter", Label: i18n.T("tui.wiz.choose"), CLI: cli, Do: (*Model).pickISP}}
	case wApps:
		cli := ""
		if w.cursor < len(w.apps) {
			verb := "enable"
			if w.chosen[w.apps[w.cursor].ID] {
				verb = "disable"
			}
			cli = "sudo splitdpi apps " + verb + " " + w.apps[w.cursor].ID
		}
		return []action{
			{Key: "space", Label: i18n.T("tui.act.toggle"), CLI: cli, Do: (*Model).toggleWizApp},
			{Key: "enter", Label: i18n.T("tui.wiz.scan"), CLI: w.scanCLI(), Do: (*Model).applyApps},
			{Key: "s", Label: i18n.T("tui.wiz.scan_site"), CLI: "sudo splitdpi scan --site DOMAIN", Do: (*Model).askSite},
		}
	case wSite:
		typed := w
		typed.site = strings.TrimSpace(w.input.Value())
		return []action{
			{Key: "enter", Label: i18n.T("tui.wiz.scan_site_go"), CLI: typed.scanCLI(), Do: (*Model).scanSite},
			{Key: "esc", Label: i18n.T("tui.act.back"), Do: func(m *Model) tea.Cmd { return nil }}, // wizardKey goes back
		}
	case wScan:
		if w.scanning {
			return []action{{Key: "esc", Label: i18n.T("tui.wiz.cancel"), CLI: w.scanCLI(), Do: (*Model).cancelScan}}
		}
		return []action{{Key: "enter", Label: i18n.T("tui.wiz.scan_again"), CLI: w.scanCLI(), Do: (*Model).startScan}}
	case wDone:
		return []action{
			{Key: "r", Label: i18n.T("tui.wiz.run_now"), CLI: "sudo splitdpi run", Do: func(m *Model) tea.Cmd { m.finishWizard(); return m.runNow() }},
			{Key: "b", Label: i18n.T("tui.wiz.at_boot"), CLI: "sudo splitdpi enable", Do: (*Model).confirmEnable},
		}
	}
	return nil
}

// confirmEnable asks before the service is installed and started.
func (m *Model) confirmEnable() tea.Cmd {
	m.dialog = &confirm{
		title:   i18n.T("shell.enable.title"),
		changes: []string{i18n.T("shell.enable.service"), i18n.T("shell.enable.boot"), i18n.T("shell.enable.rules")},
		undo:    "sudo splitdpi disable",
		yes:     (*Model).enable,
	}
	return nil
}

func (m *Model) detect() tea.Cmd {
	m.note = i18n.T("tui.wiz.looking_up", m.wiz.host)
	b, ctx := m.b, m.ctx
	return func() tea.Msg {
		info, _, err := b.Detect(ctx, 0, "")
		return detectMsg{info, err}
	}
}

func (m *Model) pickISP() tea.Cmd {
	rows := m.pickRows()
	m.wiz.info = scan.ASNInfo{}
	if c := m.wiz.cursor; c < len(rows) && len(rows[c].ASN) > 0 {
		m.wiz.info = scan.ASNInfo{ASN: rows[c].ASN[0], Country: rows[c].Country, Org: rows[c].Name}
	}
	m.chooseApps()
	return nil
}

func (m *Model) toggleWizApp() tea.Cmd {
	if w := &m.wiz; w.cursor < len(w.apps) {
		id := w.apps[w.cursor].ID
		w.chosen[id] = !w.chosen[id]
	}
	return nil
}

// applyApps saves the chosen apps, then the preset trials start.
func (m *Model) applyApps() tea.Cmd {
	if len(m.wiz.chosenList()) == 0 {
		m.note = i18n.T("tui.wiz.no_apps")
		return nil
	}
	m.wiz.site = ""
	return m.saveApps()
}

// askSite asks for the one site a blockcheck2 scan should open.
func (m *Model) askSite() tea.Cmd {
	w := &m.wiz
	w.step, w.site, m.focus, m.note, m.inPage = wSite, "", 0, "", true
	w.ipv6 = m.b.HasIPv6(m.ctx)
	w.input = textinput.New()
	w.input.Placeholder = "example.com"
	w.input.CharLimit = 253
	return w.input.Focus()
}

// scanSite saves the chosen apps, then blockcheck2 scans the typed site.
func (m *Model) scanSite() tea.Cmd {
	typed := strings.TrimSpace(m.wiz.input.Value())
	if typed == "" {
		return nil
	}
	site, err := domain.Normalize(typed)
	if err != nil {
		m.note = errText(err)
		return nil
	}
	m.wiz.site = site
	m.wiz.input.Blur()
	return m.saveApps()
}

// saveApps saves the chosen apps (`apps enable|disable`), then the scan starts.
func (m *Model) saveApps() tea.Cmd {
	b, chosen, current := m.b, m.wiz.chosen, m.cfg.Apps
	ids := make([]string, 0, len(m.wiz.apps))
	for _, a := range m.wiz.apps {
		ids = append(ids, a.ID)
	}
	return func() tea.Msg {
		for _, id := range ids {
			var err error
			switch on := slices.Contains(current, id); {
			case chosen[id] && !on:
				err = b.AppsEnable(id)
			case !chosen[id] && on:
				err = b.AppsDisable(id)
			}
			if err != nil {
				return appsAppliedMsg{err}
			}
		}
		return appsAppliedMsg{}
	}
}

func (m *Model) startScan() tea.Cmd {
	ctx, cancel := context.WithCancel(m.ctx)
	w := &m.wiz
	w.step, w.scanning, w.cancelled, w.err, w.report, w.scanCancel = wScan, true, false, nil, nil, cancel
	w.progress = &ring{max: 200}
	m.focus, m.note = 0, ""
	opt := scan.Options{ASN: w.info.ASN, Country: w.info.Country, Apps: w.chosenList(), Site: w.site}
	b, prog := m.b, w.progress
	run := func() tea.Msg {
		rep, err := b.Scan(ctx, opt, func(l string) { prog.Write([]byte(l + "\n")) })
		return scanDoneMsg{rep, err}
	}
	return batch(run, m.every(300*time.Millisecond, func(time.Time) tea.Msg { return scanTickMsg{} }))
}

func (m *Model) cancelScan() tea.Cmd {
	if m.wiz.scanning {
		m.wiz.cancelled = true
		m.wiz.scanCancel()
	}
	return nil
}

func (m *Model) enable() tea.Cmd {
	m.note = i18n.T("tui.wiz.enabling")
	b, ctx := m.b, m.ctx
	return func() tea.Msg { return enableMsg{b.Enable(ctx)} }
}

// wizardKey handles list movement, the site input and scan cancelling; false
// means not handled.
func (m Model) wizardKey(k tea.KeyMsg, s string) (Model, tea.Cmd, bool) {
	w := &m.wiz
	rows := 0
	switch w.step {
	case wSite:
		switch s {
		case "enter":
			return m, nil, false // the scan action submits
		case "esc":
			w.step, m.focus = wApps, 0
			w.input.Blur()
			return m, nil, true
		}
		var cmd tea.Cmd
		w.input, cmd = w.input.Update(k)
		return m, cmd, true
	case wPick:
		rows = len(m.pickRows()) + 1 // + "not listed"
	case wApps:
		rows = len(w.apps)
	case wScan:
		if s == "esc" && w.scanning {
			return m, m.cancelScan(), true
		}
	}
	switch s {
	case "up", "k":
		if rows > 0 {
			w.cursor = (w.cursor + rows - 1) % rows
		}
		return m, nil, rows > 0
	case "down", "j":
		if rows > 0 {
			w.cursor = (w.cursor + 1) % rows
		}
		return m, nil, rows > 0
	}
	return m, nil, false
}

// wizardView is the step's text, then its list (ISPs, apps or scan
// progress) scrolled to fit.
func (m Model) wizardView(width, h int) string {
	w := m.wiz
	var head, list []string
	cursor := w.cursor
	switch w.step {
	case wIntro:
		head = append(head, i18n.T("shell.scan.intro"))
	case wDoctor:
		head = append(head, i18n.T("tui.wiz.doctor"))
		for _, c := range w.checks {
			if c.Severity == doctor.OK {
				continue
			}
			mark := warnStyle.Render("!")
			if c.Severity == doctor.Blocker {
				mark = badStyle.Render(m.g.fail)
			}
			head = append(head, mark+" "+c.Msg)
			if c.Fix != "" {
				head = append(head, "    "+mutedStyle.Render("$ ")+commandStyle.Render(c.Fix))
			}
		}
	case wLookup:
		head = append(head, i18n.T("tui.wiz.lookup_ask", w.host))
	case wPick:
		head = append(head, i18n.T("tui.wiz.pick"))
		rows := m.pickRows()
		for i := range len(rows) + 1 {
			label := i18n.T("tui.wiz.not_listed")
			if i < len(rows) {
				label = fmt.Sprintf("%s  %s", rows[i].Name, mutedStyle.Render(rows[i].Country+" AS"+asns(rows[i].ASN)))
			}
			list = append(list, m.cursorMark(i == w.cursor)+label)
		}
	case wApps:
		if w.info.ASN != 0 {
			head = append(head, i18n.T("tui.wiz.detected", w.info.Org, w.info.ASN, w.info.Country), "")
		}
		head = append(head, i18n.T("tui.wiz.apps"))
		for i, a := range w.apps {
			list = append(list, m.cursorMark(i == w.cursor)+m.mark(w.chosen[a.ID])+" "+a.Name)
		}
	case wSite:
		ipv := i18n.T("tui.wiz.ipv4")
		if w.ipv6 {
			ipv = i18n.T("tui.wiz.ipv46")
		}
		head = append(head, i18n.T("tui.wiz.which_site"), w.input.View(), "", mutedStyle.Render(ipv))
	case wScan:
		switch {
		case w.scanning:
			head = append(head, i18n.T("tui.wiz.scanning"))
		case w.cancelled:
			head = append(head, warnStyle.Render(i18n.T("tui.wiz.cancelled")))
		case errors.Is(w.err, scan.ErrNoStrategy):
			head = append(head, badStyle.Render(i18n.T("scan.no_strategy")))
		case w.err != nil:
			head = append(head, badStyle.Render(errText(w.err)))
		}
		list = w.progress.Last(h)
		cursor = len(list) - 1
	case wDone:
		r := w.report
		head = append(head, goodStyle.Render(i18n.T("scan.winner", r.Winner.ID, r.Winner.Source)))
		if r.VoiceUnverified {
			head = append(head, warnStyle.Render(i18n.T("scan.voice")))
		}
		head = append(head, "", i18n.T("tui.wiz.how"))
	}
	text := lipgloss.NewStyle().Width(width).Render(strings.Join(head, "\n"))
	if len(list) == 0 {
		return text
	}
	return text + "\n" + strings.Join(scroll(list, cursor, h-lipgloss.Height(text), width), "\n")
}

func asns(a []int) string {
	s := make([]string, len(a))
	for i, n := range a {
		s[i] = strconv.Itoa(n)
	}
	return strings.Join(s, ",")
}

// batch is tea.Batch without nil commands; a single command stays as is.
func batch(cmds ...tea.Cmd) tea.Cmd {
	var valid []tea.Cmd
	for _, c := range cmds {
		if c != nil {
			valid = append(valid, c)
		}
	}
	switch len(valid) {
	case 0:
		return nil
	case 1:
		return valid[0]
	}
	return tea.Batch(valid...)
}
