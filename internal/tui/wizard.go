package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/KadirBerkpolat1/SplitDPI/internal/config"
	"github.com/KadirBerkpolat1/SplitDPI/internal/doctor"
	"github.com/KadirBerkpolat1/SplitDPI/internal/domain"
	"github.com/KadirBerkpolat1/SplitDPI/internal/engine"
	"github.com/KadirBerkpolat1/SplitDPI/internal/i18n"
	"github.com/KadirBerkpolat1/SplitDPI/internal/ops"
	"github.com/KadirBerkpolat1/SplitDPI/internal/presets"
	"github.com/KadirBerkpolat1/SplitDPI/internal/probe"
	"github.com/KadirBerkpolat1/SplitDPI/internal/scan"
	"github.com/KadirBerkpolat1/SplitDPI/internal/sysinfo"
)

// The setup page: the automatic setup (doctor → provider → apps → preset
// trials), a deep scan of one site (blockcheck2), or choosing a preset
// method by hand. Esc goes back one step everywhere.
type setupPage struct{ noClose }

type wizStep int

const (
	wIntro wizStep = iota // the three ways to set up
	wDoctor
	wLookup
	wPick
	wApps
	wSite
	wScan
	wDone
	wStrategy // the preset methods, chosen by hand
)

// The intro's choices, in order.
const (
	introAuto = iota
	introSite
	introManual
	introChoices
)

type wizard struct {
	step   wizStep
	intro  int // the intro's choice under the cursor
	checks []doctor.Check
	host   string
	info   scan.ASNInfo
	cursor int // picker row or app row
	apps   []presets.App
	chosen map[string]bool

	// A site scan: blockcheck2 on one domain instead of the preset trials.
	input    textinput.Model
	site     string // normalized; empty for the preset trials
	ipv6     bool
	siteFlow bool // chosen on the page: the site follows the ISP, no apps step

	installing bool
	installErr error
	installed  string // the note once the engine or packages are in, shown after the checks rerun

	pkgs           sysinfo.Packages // the missing packages, found with the checks
	pkgsSkipped    bool             // "continue without them": not offered again in this setup
	pkgsInstalling bool
	pkgsErr        error

	progress   *ring
	scanCancel context.CancelFunc
	scanning   bool
	cancelled  bool
	dnsOn      bool             // the scan turned encrypted DNS on
	asks       chan dnsQuestion // polled on each scan tick
	done       *job             // the scan, until its rules are gone and the bypass it stopped runs again
	paused     bool             // the scan stopped the bypass and starts it again with what it saved
	resumeErr  error            // that start failed
	report     *scan.Report
	err        error

	// A new setup keeps these.
	last   *lastScan // the last finished scan
	shared string    // the share link of the last strategy saved in this session
}

type lastScan struct {
	rep  scan.Report
	site string
}

type (
	doctorMsg struct {
		checks []doctor.Check
		pkgs   sysinfo.Packages
	}
	detectMsg struct {
		info scan.ASNInfo
		err  error
	}
	appsAppliedMsg struct{ err error }
	scanDoneMsg    struct {
		rep scan.Report
		err error
		// applied is the saving of the chosen apps after a winner, nil without.
		applied *appsAppliedMsg
		// runHere: the scan stopped the run in this TUI, to start again here.
		runHere   bool
		resumeErr error
		// restarted: the service or background run the scan stopped runs
		// again, with every change saved before.
		restarted bool
		// status is read once the bypass runs again, so that the done step
		// never shows the stopped one a tick read during the scan.
		status statusMsg
	}
	scanTickMsg struct{}
	engineMsg   struct {
		eng engine.Engine
		err error
	}
)

func (setupPage) Title() string                                      { return i18n.T("shell.menu.setup") }
func (setupPage) Hint() string                                       { return i18n.T("shell.hint.setup") }
func (setupPage) Open(*Model) tea.Cmd                                { return nil }
func (setupPage) Actions(m Model) []action                           { return m.wizardActions() }
func (setupPage) Update(m Model, msg tea.Msg) (Model, tea.Cmd, bool) { return m.wizardUpdate(msg) }
func (setupPage) Key(m Model, k tea.KeyMsg, s string) (Model, tea.Cmd, bool) {
	return m.wizardKey(k, s)
}
func (setupPage) View(m Model, w, h int) string { return m.wizardView(w, h) }

// openWizard starts the setup for the preset trials.
func (m *Model) openWizard() tea.Cmd { return m.startWizard(false) }

// openSiteWizard starts the setup for a blockcheck2 scan of one site.
func (m *Model) openSiteWizard() tea.Cmd { return m.startWizard(true) }

// A running scan, engine download or package install is kept: the page shows
// it instead of starting over.
func (m *Model) startWizard(site bool) tea.Cmd {
	m.note = ""
	if m.wiz.scanning || m.wiz.installing || m.wiz.pkgsInstalling {
		return nil
	}
	m.wiz = wizard{step: wDoctor, siteFlow: site, progress: &ring{max: 200}, last: m.wiz.last, shared: m.wiz.shared}
	return m.runDoctor()
}

// resetWizard goes back to the page's choice of ways.
func (m *Model) resetWizard() tea.Cmd {
	m.wiz = wizard{last: m.wiz.last, shared: m.wiz.shared, intro: m.wiz.intro}
	return nil
}

// copyLink puts the share link on the clipboard through the terminal (OSC 52).
func (m *Model) copyLink() tea.Cmd {
	m.note = i18n.T("scan_page.copied")
	out, seq := m.out, ansi.SetSystemClipboard(m.wiz.shared)
	return func() tea.Msg {
		io.WriteString(out, seq)
		return nil
	}
}

// printShare writes the share link once more, once the screen is back to normal.
func (m Model) printShare(w io.Writer) {
	if m.wiz.shared != "" {
		fmt.Fprintln(w, m.wiz.shared)
	}
}

// finishWizard leaves the finished wizard for the status page.
func (m *Model) finishWizard() {
	m.resetWizard()
	m.page = pgStatus
}

func (m *Model) runDoctor() tea.Cmd {
	b, ctx := m.b, m.ctx
	m.note = i18n.T("tui.wiz.checking")
	return func() tea.Msg { return doctorMsg{b.Doctor(ctx), b.MissingPackages()} }
}

// checked moves on to finding the provider unless the checks block or the
// missing packages are still to be offered. The lookup starts at once; n
// picks the provider by hand instead.
func (m *Model) checked() tea.Cmd {
	w := &m.wiz
	if len(blockers(w.checks)) > 0 || w.offerPkgs() {
		return nil
	}
	w.step = wLookup
	host, err := m.b.IPInfoHost()
	if err != nil {
		m.note = errText(err)
	}
	w.host = host
	return m.detect()
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

// engineMissing reports whether the checks block on the engine, which the
// wizard can install itself.
func engineMissing(cs []doctor.Check) bool {
	return slices.ContainsFunc(cs, func(c doctor.Check) bool { return c.ID == "engine" && c.Severity == doctor.Blocker })
}

// installEngine downloads and installs the pinned engine, then the checks rerun.
func (m *Model) installEngine() tea.Cmd {
	w := &m.wiz
	if w.installing {
		return nil
	}
	w.installing, w.installErr, m.note = true, nil, ""
	b, ctx, logs := m.b, m.ctx, m.logs
	return func() tea.Msg {
		fmt.Fprintln(logs, i18n.T("engine.downloading", engine.URL))
		e, err := b.EngineInstall(ctx, "")
		return engineMsg{e, err}
	}
}

// installFromCLI, then a file name, installs the engine from an archive
// fetched another way.
const installFromCLI = "sudo splitdpi engine install --from "

func (m Model) wizardUpdate(msg tea.Msg) (Model, tea.Cmd, bool) {
	w := &m.wiz
	switch msg := msg.(type) {
	case doctorMsg:
		if w.step != wDoctor {
			return m, nil, true // left with Esc meanwhile
		}
		w.checks, w.pkgs, m.note, w.installed = msg.checks, msg.pkgs, w.installed, ""
		cmd := m.checked()
		return m, cmd, true
	case pkgsMsg:
		m, cmd := m.pkgsDone(msg)
		return m, cmd, true
	case detectMsg:
		if w.step != wLookup {
			return m, nil, true // picked by hand or left meanwhile
		}
		if msg.err != nil {
			m.note = errText(msg.err)
			w.step, w.cursor = wPick, 0
			return m, nil, true
		}
		w.info = msg.info
		cmd := m.afterISP()
		return m, cmd, true
	case engineMsg:
		w.installing = false
		if msg.err != nil {
			w.installErr = msg.err
			fmt.Fprintln(m.logs, msg.err)
			return m, nil, true
		}
		w.installed = i18n.T("engine.installed", engine.Version, msg.eng.Dir)
		cmd := m.runDoctor()
		return m, cmd, true
	case appsAppliedMsg:
		if msg.err != nil {
			m.note = errText(msg.err)
		}
		return m, m.refresh(), true
	case scanTickMsg:
		if w.scanning {
			if m.ask == nil {
				select {
				case q := <-w.asks:
					m.openDNSAsk(q)
				default:
				}
			}
			return m, m.every(300*time.Millisecond, func(time.Time) tea.Msg { return scanTickMsg{} }), true
		}
		return m, nil, true
	case scanDoneMsg:
		m.applyStatus(msg.status)
		w.scanning, m.ask = false, nil
		w.scanCancel()
		if !w.cancelled && (msg.err == nil || errors.Is(msg.err, scan.ErrNoStrategy)) {
			w.last = &lastScan{rep: msg.rep, site: w.site}
		}
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
			if w.shared = ops.ShareURL(msg.rep, m.version); w.shared != "" {
				fmt.Fprintln(m.logs, w.shared)
			}
			w.step = wDone
			if w.site != "" {
				m.note = i18n.T("tui.note.site_added", w.site)
			} else if msg.applied != nil && msg.applied.err != nil {
				m.note = errText(msg.applied.err)
			}
		}
		w.resumeErr = msg.resumeErr
		if msg.resumeErr != nil {
			m.note = errText(msg.resumeErr)
		}
		if msg.restarted {
			m.pending = false
		}
		if msg.runHere && !m.quitting {
			cmd := m.runNow()
			return m, batch(cmd, m.refresh()), true
		}
		return m, nil, true
	}
	return m.strategyUpdate(msg)
}

// afterISP moves on once the ISP is known: to the apps for the preset
// trials, or straight to the site for a site scan.
func (m *Model) afterISP() tea.Cmd {
	if m.wiz.siteFlow {
		return m.askSite()
	}
	m.chooseApps()
	return nil
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
	w.step, w.cursor = wApps, 0
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

// introActions are the three ways to set up: enter takes the one under the
// cursor, and each has its own key.
func introActions() []action {
	return []action{
		{Key: "w", Label: i18n.T("tui.wiz.scan"), CLI: "sudo splitdpi scan", Do: (*Model).openWizard},
		{Key: "s", Label: i18n.T("tui.wiz.scan_site"), CLI: "sudo splitdpi scan --site DOMAIN", Do: (*Model).openSiteWizard},
		{Key: "m", Label: i18n.T("tui.wiz.manual"), CLI: "sudo splitdpi strategy list", Do: (*Model).openStrategies},
	}
}

func (m Model) wizardActions() []action {
	w := m.wiz
	back := action{Key: "esc", Label: i18n.T("tui.act.back"), Do: (*Model).wizardBack}
	switch w.step {
	case wIntro:
		acts := introActions()
		chosen := acts[w.intro]
		return append([]action{{Key: "enter", Label: i18n.T("tui.act.choose"), CLI: chosen.CLI, Do: chosen.Do}}, acts...)
	case wDoctor:
		again := action{Key: "r", Label: i18n.T("tui.wiz.check_again"), CLI: "sudo splitdpi doctor", Do: (*Model).runDoctor}
		switch {
		case w.installing, w.pkgsInstalling:
			return nil
		case w.offerPkgs(): // the engine is offered once the checks rerun after them
			return append(m.pkgsActions(again), back)
		case engineMissing(w.checks):
			return []action{{Key: "enter", Label: i18n.T("scan_page.install"), CLI: "sudo splitdpi engine install", Do: (*Model).installEngine}, again, back}
		}
		return []action{again, back}
	case wLookup:
		return []action{
			{Key: "n", Label: i18n.T("tui.wiz.pick_isp"), Do: func(m *Model) tea.Cmd { m.wiz.step, m.wiz.cursor = wPick, 0; return nil }},
			back,
		}
	case wPick:
		cli := ""
		if rows := m.pickRows(); w.cursor < len(rows) && len(rows[w.cursor].ASN) > 0 {
			isp := rows[w.cursor]
			cli = fmt.Sprintf("sudo splitdpi detect --asn %d --country %s", isp.ASN[0], isp.Country)
		}
		return []action{{Key: "enter", Label: i18n.T("tui.wiz.choose"), CLI: cli, Do: (*Model).pickISP}, back}
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
			{Key: "enter", Label: i18n.T("tui.wiz.try"), CLI: w.scanCLI(), Do: (*Model).applyApps},
			{Key: "space", Label: i18n.T("tui.act.toggle"), CLI: cli, Do: (*Model).toggleWizApp},
			{Key: "p", Label: i18n.T("tui.wiz.change_isp"), Do: func(m *Model) tea.Cmd { m.wiz.step, m.wiz.cursor = wPick, 0; return nil }},
			{Key: "s", Label: i18n.T("tui.wiz.scan_site"), CLI: "sudo splitdpi scan --site DOMAIN", Do: (*Model).askSite},
			back,
		}
	case wSite:
		typed := w
		typed.site = strings.TrimSpace(w.input.Value())
		return []action{
			{Key: "enter", Label: i18n.T("tui.wiz.scan_site_go"), CLI: typed.scanCLI(), Do: (*Model).scanSite},
			back,
		}
	case wScan:
		if w.scanning {
			return []action{{Key: "esc", Label: i18n.T("tui.wiz.cancel"), CLI: w.scanCLI(), Do: (*Model).cancelScan}}
		}
		return []action{
			{Key: "enter", Label: i18n.T("tui.wiz.scan_again"), CLI: w.scanCLI(), Do: (*Model).startScan},
			back,
		}
	case wDone:
		return m.doneActions()
	case wStrategy:
		return m.strategyActions()
	}
	return nil
}

// doneActions start the bypass with the method found, the way the run mode
// says (or restart it), offer the background and boot instead of this
// window, and copy the share link.
func (m Model) doneActions() []action {
	finish := func(do func(*Model) tea.Cmd) func(*Model) tea.Cmd {
		return func(m *Model) tea.Cmd {
			m.finishWizard()
			return do(m)
		}
	}
	var acts []action
	switch {
	case m.bypassOn() && !m.wiz.paused:
		if m.canRestart() {
			acts = append(acts, action{Key: "r", Label: i18n.T("tui.act.restart"), CLI: m.restartCLI(), Do: finish((*Model).restart)})
		}
	case m.canStart():
		a := m.startAction()
		a.Do = finish(a.Do)
		acts = append(acts, a)
		if m.runMode() == config.RunWindow {
			acts = append(acts, action{Key: "a", Label: i18n.T("background_tui.act"), CLI: runModeCLI(config.RunBackground), Do: finish((*Model).runBackground)})
		}
	}
	if m.svc.Supported && !m.svc.Enabled {
		acts = append(acts, action{Key: "b", Label: i18n.T("tui.wiz.at_boot"), CLI: "sudo splitdpi enable", Do: (*Model).confirmEnable})
	}
	if m.wiz.shared != "" {
		acts = append(acts, action{Key: "c", Label: i18n.T("scan_page.copy"), Do: (*Model).copyLink})
	}
	return append(acts, action{Key: "esc", Label: i18n.T("tui.wiz.done"), Do: (*Model).resetWizard})
}

// doneRuns is whether the done step offers a way to run the bypass.
func (m Model) doneRuns() bool {
	return slices.ContainsFunc(m.doneActions(), func(a action) bool { return a.Key != "c" && a.Key != "esc" })
}

// wizardBack goes back one step: to the step before, or to the intro.
func (m *Model) wizardBack() tea.Cmd {
	w := &m.wiz
	switch {
	case w.installing || w.pkgsInstalling || w.scanning:
		return nil
	case w.step == wPick && len(w.apps) > 0:
		w.step, w.cursor = wApps, 0
	case w.step == wSite && !w.siteFlow:
		w.step = wApps
		w.input.Blur()
	default:
		return m.resetWizard()
	}
	return nil
}

// detect asks the lookup service for the provider; the log names it.
func (m *Model) detect() tea.Cmd {
	fmt.Fprintln(m.logs, i18n.T("tui.wiz.looking_up", m.wiz.host))
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
	if m.wiz.siteFlow || len(m.wiz.apps) == 0 {
		return m.afterISP()
	}
	m.wiz.step, m.wiz.cursor = wApps, 0 // the apps chosen so far stay
	return nil
}

func (m *Model) toggleWizApp() tea.Cmd {
	if w := &m.wiz; w.cursor < len(w.apps) {
		id := w.apps[w.cursor].ID
		w.chosen[id] = !w.chosen[id]
	}
	return nil
}

// applyApps starts the preset trials for the chosen apps; they are saved
// once a method opens them.
func (m *Model) applyApps() tea.Cmd {
	if len(m.wiz.chosenList()) == 0 {
		m.note = i18n.T("tui.wiz.no_apps")
		return nil
	}
	m.wiz.site = ""
	return m.startScan()
}

// askSite asks for the one site a blockcheck2 scan should open.
func (m *Model) askSite() tea.Cmd {
	w := &m.wiz
	w.step, w.site, m.note = wSite, "", ""
	w.ipv6 = m.b.HasIPv6(m.ctx)
	w.input = m.newInput()
	w.input.Placeholder = "example.com"
	w.input.CharLimit = 253
	return w.input.Focus()
}

// scanSite runs blockcheck2 on the typed site; the apps are left as they are.
func (m *Model) scanSite() tea.Cmd {
	if m.scanWaits() {
		return nil
	}
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
	return m.startScan()
}

// saveApps saves the apps chosen for the trials (`apps enable|disable`):
// the new ones first, so that mode apps never runs out of apps between two
// changes.
func (m *Model) saveApps() tea.Cmd {
	b, chosen, current := m.b, m.wiz.chosen, m.cfg.Apps
	var enable, disable []string
	for _, a := range m.wiz.apps {
		switch on := slices.Contains(current, a.ID); {
		case chosen[a.ID] && !on:
			enable = append(enable, a.ID)
		case !chosen[a.ID] && on:
			disable = append(disable, a.ID)
		}
	}
	return func() tea.Msg {
		for _, id := range enable {
			if err := b.AppsEnable(id); err != nil {
				return appsAppliedMsg{err}
			}
		}
		for _, id := range disable {
			if err := b.AppsDisable(id); err != nil {
				return appsAppliedMsg{err}
			}
		}
		return appsAppliedMsg{}
	}
}

// startScan runs the trials. A bypass running as the service, in the
// background or in this TUI holds the queue they need: it stops for the scan
// and starts again the same way after, once the chosen apps are saved. A run
// in another terminal stays, and the scan refuses to start.
func (m *Model) startScan() tea.Cmd {
	if m.scanWaits() {
		return nil
	}
	ctx, cancel := context.WithCancel(m.ctx)
	w := &m.wiz
	w.step, w.scanning, w.cancelled, w.dnsOn, w.err, w.report, w.resumeErr, w.scanCancel = wScan, true, false, false, nil, nil, nil, cancel
	done := newJob()
	w.progress, w.asks, w.done = &ring{max: 200}, make(chan dnsQuestion), done
	m.note = ""
	b, prog, logs, asks, g, site := m.b, w.progress, m.logs, w.asks, m.g, w.site
	opt := scan.Options{
		ASN: w.info.ASN, Country: w.info.Country, Apps: w.chosenList(), Site: w.site,
		DNSCheck: func(ctx context.Context, poisoned []probe.DNSResult) string { return askDNS(ctx, asks, poisoned) },
		Trial:    func(tr scan.Trial) { writeTrial(prog, logs, g, tr) },
	}
	if w.site != "" {
		opt.Apps = nil
	}
	save := m.saveApps()
	runHere, mode := m.running, ""
	if !runHere && m.stateOn() && (m.st.Mode == "service" || m.st.Mode == "background") {
		mode = m.st.Mode
	}
	stopHere := m.stopOwnRun()
	w.paused = runHere || mode != ""
	scanRun := func() scanDoneMsg {
		if runHere || mode != "" {
			prog.Write([]byte(i18n.T("scan_page.paused") + "\n"))
		}
		stopHere()
		if mode != "" {
			if err := b.Stop(); err != nil {
				return scanDoneMsg{err: err, runHere: runHere}
			}
		}
		rep, err := b.Scan(ctx, opt, func(l string) { prog.Write([]byte(l + "\n")) })
		msg := scanDoneMsg{rep: rep, err: err, runHere: runHere}
		// The apps chosen for the trials are saved once a strategy opens them.
		if err == nil && rep.Winner != nil && site == "" {
			a := save().(appsAppliedMsg)
			msg.applied = &a
		}
		msg.resumeErr = resume(context.WithoutCancel(ctx), b, mode)
		msg.restarted = mode != "" && msg.resumeErr == nil
		return msg
	}
	run := func() tea.Msg {
		var msg scanDoneMsg
		if !done.run(func() { msg = scanRun(); msg.status = readStatus(b) }) {
			return nil
		}
		return msg
	}
	return batch(run, m.every(300*time.Millisecond, func(time.Time) tea.Msg { return scanTickMsg{} }))
}

// scanWaits is whether a scan has to wait, and then says why: a restart, a
// service change or a background start is under way, whose bypass the scan
// would stop halfway or run beside.
func (m *Model) scanWaits() bool {
	if m.waitHeld() {
		return true
	}
	if m.svcOp != nil || m.bgStarting() {
		m.note = i18n.T("fix2_arayuz.start_wait")
		return true
	}
	return false
}

// resume starts the bypass the scan stopped again, the way it ran: mode is
// the stopped run's, "" when the scan stopped none.
func resume(ctx context.Context, b Backend, mode string) error {
	var err error
	switch mode {
	case "service":
		_, err = b.Enable(ctx, nil)
	case "background":
		_, err = b.Background(ctx)
	}
	return err
}

// writeTrial lists a finished trial and why each probe failed in plain
// words; the raw errors go to the log.
func writeTrial(prog, logs io.Writer, g glyphs, tr scan.Trial) {
	mark := g.fail
	if tr.OK {
		mark = g.on
	}
	id := tr.Candidate.Strategy.ID
	fmt.Fprintf(prog, "%s %s\n", mark, id)
	for _, r := range tr.Results {
		if r.OK {
			continue
		}
		host := r.URL
		if u, err := url.Parse(r.URL); err == nil && u.Hostname() != "" {
			host = u.Hostname()
		}
		fmt.Fprintf(prog, "    %s: %s\n", host, probe.Classify(r).What())
		fmt.Fprintf(logs, "%s  %s  %s\n", id, r.URL, r.Err)
	}
}

func (m *Model) cancelScan() tea.Cmd {
	if m.wiz.scanning {
		m.wiz.cancelled = true
		m.wiz.scanCancel()
	}
	return nil
}

// wizardKey moves in the step's list, feeds the site input, and takes esc
// as back (or as cancel while a scan runs).
func (m Model) wizardKey(k tea.KeyMsg, s string) (Model, tea.Cmd, bool) {
	w := &m.wiz
	rows := 0
	switch w.step {
	case wIntro:
		rows = introChoices
		switch s {
		case "up", "k":
			w.intro = (w.intro + rows - 1) % rows
			return m, nil, true
		case "down", "j":
			w.intro = (w.intro + 1) % rows
			return m, nil, true
		}
		return m, nil, false
	case wSite:
		switch s {
		case "enter":
			return m, nil, false // the scan action submits
		case "esc":
			cmd := m.wizardBack()
			return m, cmd, true
		}
		var cmd tea.Cmd
		w.input, cmd = w.input.Update(k)
		return m, cmd, true
	case wStrategy:
		return m.strategyKey(s)
	case wPick:
		rows = len(m.pickRows()) + 1 // + "not listed"
	case wApps:
		rows = len(w.apps)
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
		return m.introView(width, h)
	case wStrategy:
		return m.strategyView(width, h)
	case wDoctor:
		offer := w.offerPkgs()
		switch {
		case w.checks == nil:
			head = append(head, i18n.T("tui.wiz.checking"))
		case offer && len(blockers(w.checks)) == 0:
			head = append(head, i18n.T("tui.pkgs.head"))
		default:
			head = append(head, i18n.T("tui.wiz.doctor"))
		}
		for _, c := range w.checks {
			if c.Severity == doctor.OK {
				continue
			}
			mark := warnStyle.Render("!")
			if c.Severity == doctor.Blocker {
				mark = badStyle.Render(m.g.fail)
			}
			head = append(head, mark+" "+c.Msg)
			// The package summary below replaces the fixes of the tools it installs.
			if c.Fix != "" && !(offer && w.offersTool(c.ID)) {
				head = append(head, "    "+mutedStyle.Render("$ ")+commandStyle.Render(c.Fix))
			}
		}
		if offer {
			head = append(head, w.pkgsView()...)
		} else if engineMissing(w.checks) {
			switch {
			case w.installing:
				head = append(head, "", i18n.T("scan_page.installing", engine.Version))
			case w.installErr != nil:
				head = append(head, "", badStyle.Render(ops.EngineInstallText(w.installErr)), i18n.T("engine.download_hint", engine.URL),
					"    "+mutedStyle.Render("$ ")+commandStyle.Render(installFromCLI+i18n.T("scan_page.file")))
			default:
				head = append(head, "", i18n.T("scan_page.install_offer", engine.Version))
			}
		}
	case wLookup:
		head = append(head, i18n.T("tui.wiz.lookup"), "", mutedStyle.Render(i18n.T("tui.wiz.lookup_note")))
	case wPick:
		head = append(head, i18n.T("tui.wiz.pick"))
		rows := m.pickRows()
		for i := range len(rows) + 1 {
			label := i18n.T("tui.wiz.not_listed")
			if i < len(rows) {
				label = rows[i].Name + "  " + mutedStyle.Render(rows[i].Country)
			}
			list = append(list, m.listMark(i == w.cursor)+label)
		}
	case wApps:
		isp := w.info.Org
		if isp == "" {
			isp = mutedStyle.Render(i18n.T("tui.isp_unknown"))
		}
		head = append(head, row(width, i18n.T("tui.isp"), isp), "", i18n.T("tui.wiz.apps"))
		for i, a := range w.apps {
			list = append(list, m.listMark(i == w.cursor)+m.mark(w.chosen[a.ID])+" "+a.Name)
		}
	case wSite:
		ipv := i18n.T("tui.wiz.ipv4")
		if w.ipv6 {
			ipv = i18n.T("tui.wiz.ipv46")
		}
		head = append(head, i18n.T("tui.wiz.which_site"), ansi.Truncate(w.input.View(), width, ""), "", mutedStyle.Render(ipv))
	case wScan:
		switch {
		case w.scanning:
			head = append(head, i18n.T("tui.wiz.scanning"))
		case w.cancelled && w.resumeErr != nil:
			head = append(head, badStyle.Render(i18n.T("fix_arayuz.cancelled_not_restarted")))
		case w.cancelled && w.dnsOn:
			head = append(head, warnStyle.Render(i18n.T("tui.wiz.cancelled_dns")))
		case w.cancelled:
			head = append(head, warnStyle.Render(i18n.T("tui.wiz.cancelled")))
		case errors.Is(w.err, scan.ErrNoStrategy):
			head = append(head, badStyle.Render(i18n.T("tui.wiz.none_found")))
		case w.err != nil:
			head = append(head, badStyle.Render(errText(w.err)))
		}
		for _, l := range w.progress.Last(h) {
			list = append(list, wrapIndented(l, width)...)
		}
		cursor = len(list) - 1
	case wDone:
		r := w.report
		head = append(head, goodStyle.Bold(true).Render(i18n.T("tui.wiz.found")), mutedStyle.Render(r.Winner.ID))
		// The link stays whole, on lines of its own, for copying.
		if w.shared != "" {
			head = append(head, "", i18n.T("scan_page.share"), ansi.Hardwrap(w.shared, width, false))
		}
		// The question repeats the hint bar's keys to run the bypass; it gives
		// way to the link, and goes when the bar offers none.
		how := append(slices.Clone(head), "", i18n.T("tui.wiz.how"))
		if m.doneRuns() && lipgloss.Height(lipgloss.NewStyle().Width(width).Render(strings.Join(how, "\n"))) <= h {
			head = how
		}
	}
	text := lipgloss.NewStyle().Width(width).Render(strings.Join(head, "\n"))
	if len(list) == 0 {
		return text
	}
	room := h - lipgloss.Height(text)
	if w.step == wScan { // a log tail, not a list to choose from
		return text + "\n" + strings.Join(scroll(list, cursor, room, width), "\n")
	}
	return text + "\n" + strings.Join(m.scrollMore(list, nil, cursor, room, width), "\n")
}

// introView offers the three ways to set up, each with what it does, then
// the method in use and the last scan.
func (m Model) introView(width, h int) string {
	w := m.wiz
	lines := strings.Split(lipgloss.NewStyle().Width(width).Render(i18n.T("shell.scan.intro")), "\n")
	lines = append(lines, "")
	notes := []string{i18n.T("scan_page.quick_note"), i18n.T("scan_page.site_note"), i18n.T("scan_page.manual_note")}
	for i, a := range introActions() {
		label := a.Label
		if i == w.intro {
			label = groupStyle.Render(label)
		}
		lines = append(lines, m.listMark(i == w.intro)+label)
		lines = append(lines, wrapIndented("    "+mutedStyle.Render(notes[i]), width)...)
	}
	if s := m.cfg.Strategy; s != nil || w.last != nil {
		lines = append(lines, "")
		if s != nil {
			lines = append(lines, row(width, i18n.T("tui.method"), m.methodName(s)))
		}
		if l := w.last; l != nil {
			lines = append(lines, row(width, i18n.T("scan_page.last"), l.summary()))
		}
	}
	return strings.Join(scroll(lines, 0, h, width), "\n")
}

// methodName says where the method in use came from, in words, with its id.
func (m Model) methodName(s *config.Strategy) string {
	name := i18n.T("tui.method_scan")
	if s.Source != "blockcheck2" {
		name = i18n.T("tui.method_preset")
		if m.ispName != "" {
			name = i18n.T("tui.method_isp", m.ispName)
		}
	}
	return name + mutedStyle.Render("  "+s.ID)
}

// wrapIndented wraps s to width, continuing under its leading spaces.
func wrapIndented(s string, width int) []string {
	text := strings.TrimLeft(s, " ")
	pad := strings.Repeat(" ", len(s)-len(text))
	lines := strings.Split(ansi.Wrap(text, max(width-len(pad), 10), ""), "\n")
	for i := range lines {
		lines[i] = pad + lines[i]
	}
	return lines
}

func (l lastScan) summary() string {
	s := i18n.N("scan_page.last_none", len(l.rep.Trials))
	if l.rep.Winner != nil {
		s = i18n.N("scan_page.last_won", len(l.rep.Trials), l.rep.Winner.ID)
	}
	if l.site != "" {
		s += mutedStyle.Render("  " + l.site)
	}
	return s
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
