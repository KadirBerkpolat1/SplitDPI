package tui

import (
	"context"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/KadirBerkpolat1/SplitDPI/internal/config"
	"github.com/KadirBerkpolat1/SplitDPI/internal/i18n"
	"github.com/KadirBerkpolat1/SplitDPI/internal/probe"
)

// The status page: is the bypass running, for what; start, stop, restart,
// start at boot, try the sites. The technical details fold away.
type statusPage struct{ noClose }

type (
	runDoneMsg struct{ err error }
	bgDoneMsg  struct {
		pid int
		err error
	}
	stopMsg  struct{ err error }
	probeMsg struct {
		res   map[string][]probe.Result
		names map[string]string // the probed apps' names by id
		err   error
	}
	// restartStoppedMsg: the bypass that ran in mode stopped, to start again.
	restartStoppedMsg struct {
		mode string
		err  error
	}
)

func (statusPage) Title() string         { return i18n.T("shell.menu.status") }
func (statusPage) Hint() string          { return i18n.T("shell.hint.status") }
func (statusPage) Open(m *Model) tea.Cmd { return m.detectForeign() }

// Key lets r restart too while the bypass runs: start is not offered then.
func (statusPage) Key(m Model, _ tea.KeyMsg, s string) (Model, tea.Cmd, bool) {
	if a, ok := m.restartAction(); ok && s == "r" {
		cmd := a.Do(&m)
		return m, cmd, true
	}
	return m, nil, false
}

func (m Model) bypassOn() bool { return m.running || m.stateOn() }

// stateOn is whether the status has a bypass running: "restarting" is one
// whose engine crashed and starts again, with the run still held.
func (m Model) stateOn() bool { return m.st.State == "running" || m.st.State == "restarting" }

// canStart is whether the bypass can be started here: it is off and no
// background start is under way.
func (m Model) canStart() bool { return !m.bypassOn() && !m.bgStarting }

// held is whether starting, stopping and restarting wait: a setup scan
// stopped the bypass or runs its trials, which nothing may stop or run
// beside, or a restart is under way.
func (m Model) held() bool { return m.wiz.scanning || m.restarting }

// Actions offer the setup first while there is no strategy to run with.
func (statusPage) Actions(m Model) []action {
	var acts []action
	if a, ok := m.takeoverAction(); ok {
		acts = append(acts, a)
	}
	if m.cfg.Strategy == nil {
		acts = append(acts, action{Key: "w", Label: i18n.T("tui.act.setup"), CLI: "sudo splitdpi scan", Do: func(m *Model) tea.Cmd {
			return batch(m.selectPage(pgSetup), m.openWizard())
		}})
	}
	switch {
	case m.held():
	case m.bypassOn():
		acts = append(acts, m.stopAction())
	case m.canStart() && m.cfg.Strategy != nil:
		acts = append(acts, m.startAction())
	}
	if a, ok := m.bootAction(); ok {
		acts = append(acts, a)
	}
	details := i18n.T("tui.act.details_show")
	if m.details {
		details = i18n.T("tui.act.details_hide")
	}
	return append(acts,
		action{Key: "p", Label: i18n.T("tui.act.probe"), CLI: "sudo splitdpi probe", Do: (*Model).probeApps},
		action{Key: "d", Label: details, CLI: "sudo splitdpi status", Do: func(m *Model) tea.Cmd { m.details = !m.details; return nil }},
	)
}

func (m Model) stopAction() action {
	return action{Key: "s", Label: i18n.T("tui.act.stop"), CLI: "sudo splitdpi stop", Do: (*Model).stop}
}

// startAction is r: it starts the bypass the way the run mode in Settings
// says. In run mode boot it starts the service, which it first sets up.
func (m Model) startAction() action {
	mode := m.runMode()
	a := action{Key: "r", Label: i18n.T("tui.act.start"), CLI: runModeCLI(mode), Do: (*Model).runNow}
	switch {
	case mode == config.RunBackground:
		a.Do = (*Model).runBackground
	case mode == config.RunBoot && m.svc.Enabled:
		a.Do = (*Model).enable
	case mode == config.RunBoot:
		a.Do = (*Model).confirmEnable
	}
	return a
}

// bootAction is b: start at boot, or no longer. It needs a service on this
// init, and a strategy to start with; a setup scan holds it, as it holds
// starting.
func (m Model) bootAction() (action, bool) {
	if !m.svc.Supported || m.cfg.Strategy == nil || m.wiz.scanning {
		return action{}, false
	}
	if m.svc.Enabled {
		return action{Key: "b", Label: i18n.T("boot.act_off"), CLI: "sudo splitdpi disable", Do: func(m *Model) tea.Cmd {
			return m.confirmDisable(config.RunBackground)
		}}, true
	}
	return action{Key: "b", Label: i18n.T("boot.act_on"), CLI: "sudo splitdpi enable", Do: (*Model).confirmEnable}, true
}

func (statusPage) Update(m Model, msg tea.Msg) (Model, tea.Cmd, bool) {
	switch msg := msg.(type) {
	case runDoneMsg:
		m.running, m.runCancel = false, nil
		if msg.err != nil {
			m.note = errText(msg.err)
		}
		if m.quitting && m.toBackground {
			m.toBackground = false
			cmd := m.runBackground()
			return m, cmd, true
		}
		if m.quitting {
			return m, tea.Quit, true
		}
		if m.restarting {
			m.restarting = false
			cmd := m.runNow()
			return m, batch(cmd, m.refresh()), true
		}
		return m, m.refresh(), true
	case enableMsg:
		next, cmd := m.enabled(msg.err)
		return next, cmd, true
	case bgDoneMsg:
		m.bgStarting = false
		if msg.err != nil {
			// A failed move to the background stays, so that the failure is seen.
			m.note, m.quitting = errText(msg.err), false
			return m, m.refresh(), true
		}
		m.note = i18n.T("background_tui.started", msg.pid)
		if m.quitting {
			m.bgPID = msg.pid
			return m, tea.Quit, true
		}
		cmd := batch(m.refresh(), m.restartLogs())
		return m, cmd, true
	case stopMsg:
		if msg.err != nil {
			m.note = errText(msg.err)
		}
		return m, m.refresh(), true
	case restartStoppedMsg:
		m.restarting = false
		if msg.err != nil {
			m.note = errText(msg.err)
			return m, m.refresh(), true
		}
		m.st.State, m.pending = "stopped", false
		var cmd tea.Cmd
		switch msg.mode {
		case "service":
			cmd = m.enable()
		case "background":
			cmd = m.runBackground()
		default:
			cmd = m.startAction().Do(&m)
		}
		return m, batch(cmd, m.refresh()), true
	case probeMsg:
		m.probe, m.probeNames, m.note = msg.res, msg.names, ""
		if msg.err != nil {
			m.note = errText(msg.err)
		}
		return m, nil, true
	}
	return m.takeoverUpdate(msg)
}

// runNow runs the bypass inside this process, like `splitdpi run` in a
// terminal: quitting the TUI stops it and removes the rules.
func (m *Model) runNow() tea.Cmd {
	if m.running {
		return nil
	}
	ctx, cancel := context.WithCancel(m.ctx)
	j := newJob()
	m.running, m.runCancel, m.runJob, m.note, m.pending = true, cancel, j, "", false
	b := m.b
	return func() tea.Msg {
		var err error
		j.run(func() { err = b.Run(ctx, false) })
		return runDoneMsg{err}
	}
}

// runBackground starts the bypass as its own process, like `splitdpi run
// --background`: it outlives the TUI and its terminal, until reboot.
func (m *Model) runBackground() tea.Cmd {
	if m.bgStarting {
		return nil
	}
	m.bgStarting, m.note, m.pending = true, i18n.T("background_tui.starting"), false
	b, ctx := m.b, m.ctx
	return func() tea.Msg {
		pid, err := b.Background(ctx)
		return bgDoneMsg{pid, err}
	}
}

// printBackground tells, once the screen is back to normal, that the bypass
// still runs after quitting and how to stop it.
func (m Model) printBackground(w io.Writer) {
	if m.bgPID != 0 {
		fmt.Fprintln(w, i18n.T("background.started", m.bgPID))
	}
}

// stop stops the bypass; a failure to stop leaves the last probe alone.
func (m *Model) stop() tea.Cmd {
	if m.running {
		m.runCancel() // runDoneMsg follows once the rules are gone
		return nil
	}
	b := m.b
	return func() tea.Msg { return stopMsg{b.Stop()} }
}

// restart stops the bypass and starts it again the same way, so that it
// picks up the changes saved since it started. Until it runs again, R and r
// wait.
func (m *Model) restart() tea.Cmd {
	m.note, m.restarting = i18n.T("tui.restarting"), true
	if m.running {
		m.runCancel() // runDoneMsg starts it again
		return nil
	}
	b, mode := m.b, m.st.Mode
	return func() tea.Msg { return restartStoppedMsg{mode, b.Stop()} }
}

// restartCLI is the command that restarts the bypass the way it runs now.
func (m Model) restartCLI() string {
	if m.running {
		return "sudo splitdpi stop && sudo splitdpi run"
	}
	return m.b.RestartCommand()
}

func (m *Model) probeApps() tea.Cmd {
	m.note = i18n.T("tui.probing")
	b, ctx, apps, w := m.b, m.ctx, m.cfg.Apps, m.logs
	return func() tea.Msg {
		res, err := b.Probe(ctx, apps)
		set := b.PresetsList()
		names := map[string]string{}
		for _, id := range slices.Sorted(maps.Keys(res)) {
			if a, ok := set.Apps[id]; ok {
				names[id] = a.Name
			}
			for _, r := range res[id] {
				if !r.OK {
					fmt.Fprintf(w, "%s: %s\n", r.URL, r.Err)
				}
			}
		}
		return probeMsg{res, names, err}
	}
}

// probeTodo is what to do about a failed probe, with the TUI's pages
// instead of commands.
func probeTodo(c probe.Category) string { return i18n.T("probeerr_tui." + string(c)) }

// probeSummary counts the probed apps and sites that open, are blocked, or
// have no address to test, under a bar of the three if bar; it names the
// first names blocked ones, apps first, and says what to do about the first.
func (m Model) probeSummary(w, names int, bar bool) []string {
	if len(m.probe) == 0 {
		return nil
	}
	type blockedSite struct {
		name string
		app  bool
		cat  probe.Category
	}
	var blocked []blockedSite
	open, untested := 0, 0
	for id, rs := range m.probe {
		switch c := probe.Overall(rs); c {
		case probe.None:
			open++
		case probe.NoAddress:
			untested++
		default:
			name, app := m.probeNames[id]
			if !app {
				name = id
			}
			blocked = append(blocked, blockedSite{name, app, c})
		}
	}
	slices.SortFunc(blocked, func(a, b blockedSite) int {
		if a.app != b.app {
			if a.app {
				return -1
			}
			return 1
		}
		return strings.Compare(a.name, b.name)
	})
	parts := []string{goodStyle.Render(i18n.T("probe_summary.open", open, len(m.probe)))}
	if len(blocked) > 0 {
		parts = append(parts, badStyle.Render(i18n.T("probe_summary.blocked", len(blocked))))
	}
	if untested > 0 {
		parts = append(parts, neutralStyle.Render(i18n.T("probe_summary.untested", untested)))
	}
	value := w - labelStyle.GetWidth()
	counts := joinWrapped(parts, mutedStyle.Render(", "), value)
	lines := []string{row(w, i18n.T("tui.probe"), counts)}
	if bar {
		lines = []string{
			row(w, i18n.T("tui.probe"), m.reachBar([3]int{open, len(blocked), untested}, min(value, 40))),
			row(w, "", counts),
		}
	}
	shown := blocked[:min(len(blocked), names)]
	for _, b := range shown {
		lines = append(lines, row(w, "", badStyle.Render(m.g.fail)+" "+b.name+"  "+badStyle.Render(i18n.T("probe_short."+string(b.cat)))))
	}
	if n := len(blocked) - len(shown); n > 0 {
		lines = append(lines, row(w, "", mutedStyle.Render(i18n.T("probe_summary.more", n))))
	}
	if len(blocked) > 0 {
		lines = append(lines, row(w, i18n.T("probe_tui.todo"), probeTodo(blocked[0].cat)))
	}
	return lines
}

// reachBar is a bar width cells wide, split in proportion to the open,
// blocked and untested counts.
func (m Model) reachBar(counts [3]int, width int) string {
	styles := [3]lipgloss.Style{goodStyle, badStyle, neutralStyle}
	var b strings.Builder
	for i, n := range barCells(counts, width) {
		b.WriteString(styles[i].Render(strings.Repeat(m.g.bar[i], n)))
	}
	return b.String()
}

// barCells splits width cells in proportion to counts. A non-zero count gets
// at least one cell; the rounding goes to the largest count.
func barCells(counts [3]int, width int) [3]int {
	var cells [3]int
	total := counts[0] + counts[1] + counts[2]
	if total == 0 {
		return cells
	}
	used, big := 0, 0
	for i, n := range counts {
		cells[i] = n * width / total
		if n > 0 && cells[i] == 0 {
			cells[i] = 1
		}
		used += cells[i]
		if n > counts[big] {
			big = i
		}
	}
	cells[big] += width - used
	return cells
}

// joinWrapped joins parts with sep in lines of at most width cells, breaking
// only between parts.
func joinWrapped(parts []string, sep string, width int) string {
	var lines []string
	cur := ""
	for _, p := range parts {
		switch {
		case cur == "":
			cur = p
		case lipgloss.Width(cur+sep+p) > width:
			lines, cur = append(lines, cur), p
		default:
			cur += sep + p
		}
	}
	return strings.Join(append(lines, cur), "\n")
}

// statusFit is what the status page shows when it is short on rows. The
// fits are tried in order, each giving up the least needed thing left.
type statusFit struct {
	spacers bool // blank lines between the parts
	names   int  // blocked sites named
	bar     bool // the reachability bar over the counts
	details bool // the details, when shown
}

var statusFits = []statusFit{
	{true, 3, true, true},
	{false, 3, true, true},
	{false, 1, true, true},
	{false, 1, false, true},
	{false, 1, false, false},
}

// View draws the first fit that takes at most h lines. If none does, the
// last one is cut to h lines and ends with an ellipsis, so that nothing goes
// missing unseen.
func (statusPage) View(m Model, w, h int) string {
	var lines []string
	for _, fit := range statusFits {
		lines = strings.Split(m.statusView(w, fit), "\n")
		if len(lines) <= h {
			break
		}
	}
	if len(lines) > h {
		lines = append(lines[:max(h-1, 0)], mutedStyle.Render(m.g.ellipsis))
	}
	return strings.Join(lines, "\n")
}

// headline is the bypass state in a word or two, and how it runs or how to
// start it. A setup scan shows itself instead of the trial or the stopped
// bypass its status has.
func (m Model) headline() (string, string) {
	big := lipgloss.NewStyle().Bold(true)
	switch {
	case m.wiz.scanning:
		return big.Foreground(warn).Render(m.g.dot + " " + i18n.T("fix_arayuz.pill_scanning")), i18n.T("tui.how.scan")
	case !m.running && m.st.State == "restarting":
		return big.Foreground(warn).Render(m.g.dot + " " + i18n.T("fix_arayuz.pill_restarting")), i18n.T("fix_arayuz.how_restarting")
	case m.bypassOn():
		how := i18n.T("tui.how." + m.st.Mode)
		if m.running {
			how = i18n.T("tui.how.here")
		}
		return big.Foreground(good).Render(m.g.dot + " " + i18n.T("shell.pill.running")), how
	case m.st.State == "failed":
		return big.Foreground(bad).Render(m.g.dot + " " + i18n.T("tui.state_failed")), i18n.T("tui.how.failed")
	case m.cfg.Strategy == nil:
		return big.Render(m.g.dotOff + " " + i18n.T("tui.state_new")), i18n.T("tui.how.setup")
	case m.bgStarting:
		return big.Render(m.g.dotOff + " " + i18n.T("shell.pill.stopped")), i18n.T("background_tui.starting")
	}
	return big.Render(m.g.dotOff + " " + i18n.T("shell.pill.stopped")), i18n.T("tui.how.start_" + string(m.runMode()))
}

func (m Model) statusView(w int, fit statusFit) string {
	wrap := lipgloss.NewStyle().Width(w)
	spacer := func(lines []string) []string {
		if fit.spacers {
			return append(lines, "")
		}
		return lines
	}
	lines := m.takeoverView(w)
	if n := len(lines); n > 0 && !fit.spacers {
		lines = lines[:n-1]
	}
	head, how := m.headline()
	lines = append(lines, head)
	lines = append(lines, strings.Split(wrap.Foreground(muted).Render(how), "\n")...)
	if m.pending && m.bypassOn() && !m.wiz.scanning {
		pending := i18n.T("tui.pending", restartKey)
		if m.otherTerminal() {
			pending = i18n.T("fix_arayuz.pending_foreground")
		}
		lines = append(lines, strings.Split(wrap.Foreground(warn).Render(pending), "\n")...)
	}
	lines = spacer(lines)

	if m.cfg.Strategy != nil {
		isp := mutedStyle.Render(i18n.T("tui.isp_unknown"))
		if m.ispName != "" {
			isp = m.ispName
		}
		lines = append(lines, row(w, i18n.T("tui.isp"), isp))
	}
	lines = append(lines, row(w, i18n.T("tui.apps"), m.appsValue()))
	lines = append(lines, row(w, i18n.T("tui.dns"), m.dnsStatus()))
	if m.svc.Supported {
		lines = append(lines, row(w, i18n.T("boot.status_label"), bootValue(m)))
	}
	if p := m.probeSummary(w, fit.names, fit.bar); len(p) > 0 {
		lines = append(spacer(lines), p...)
	}
	if m.details && fit.details {
		lines = append(spacer(lines), groupStyle.Render(i18n.T("tui.details")))
		lines = append(lines, m.detailRows(w)...)
	}
	if m.stErr != nil {
		lines = append(lines, strings.Split(wrap.Foreground(bad).Render(m.stErr.Error()), "\n")...)
	}
	return strings.Join(lines, "\n")
}

// appsValue names the enabled apps; in broad scope the bypass handles the
// other blocked sites it finds too, and says so.
func (m Model) appsValue() string {
	names := make([]string, len(m.cfg.Apps))
	for i, id := range m.cfg.Apps {
		names[i] = id
		if n, ok := m.appNames[id]; ok {
			names[i] = n
		}
	}
	v := strings.Join(names, ", ")
	if len(names) == 0 {
		v = mutedStyle.Render(i18n.T("tui.none"))
	}
	if m.mode() == config.ModeBroad {
		v += mutedStyle.Render(i18n.T("tui.apps_broad"))
	}
	return v
}

// dnsStatus is whether DNS goes encrypted, and through whom.
func (m Model) dnsStatus() string {
	if !m.cfg.DNSOn() {
		return mutedStyle.Render(i18n.T("tui.dns_plain"))
	}
	return i18n.T("tui.dns_encrypted", m.dnsName(m.cfg.DNSProvider)) + mutedStyle.Render(" ("+m.cfg.DNSProtocol.Name()+")")
}

// detailRows are what a curious user or a bug report needs: the strategy
// and where it came from, the engine, the process and the last error.
func (m Model) detailRows(w int) []string {
	var lines []string
	if s := m.cfg.Strategy; s != nil {
		lines = append(lines, row(w, i18n.T("tui.strategy"), s.ID+mutedStyle.Render("  "+s.Source)))
	}
	if m.st.Engine != "" {
		lines = append(lines, row(w, i18n.T("tui.engine"), "zapret2 "+m.st.Engine))
	}
	if m.bypassOn() && !m.running {
		proc := i18n.T("tui.process", i18n.Enum("mode", m.st.Mode), m.st.PID)
		lines = append(lines, row(w, i18n.T("tui.proc"), proc))
	}
	if m.st.LastError != "" {
		lines = append(lines, row(w, i18n.T("tui.last_error"), badStyle.Render(m.st.LastError)))
	}
	return append(lines, row(w, i18n.T("tui.version"), appName+" "+m.version))
}
