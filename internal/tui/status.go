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

// The status page: is the bypass running and with what; run, stop, probe.
type statusPage struct{}

type (
	runDoneMsg struct{ err error }
	bgDoneMsg  struct {
		pid int
		err error
	}
	probeMsg struct {
		res   map[string][]probe.Result
		names map[string]string // the probed apps' names by id
		err   error
	}
)

func (statusPage) Title() string                                              { return i18n.T("shell.menu.status") }
func (statusPage) Hint() string                                               { return i18n.T("shell.hint.status") }
func (statusPage) Open(m *Model) tea.Cmd                                      { return m.detectForeign() }
func (statusPage) Key(m Model, _ tea.KeyMsg, _ string) (Model, tea.Cmd, bool) { return m, nil, false }

func (m Model) bypassOn() bool { return m.running || m.st.State == "running" }

// canStart is whether the bypass can be started here: it is off and no
// background start is under way.
func (m Model) canStart() bool { return !m.bypassOn() && !m.bgStarting }

// Actions offer the setup first while there is no strategy to run with.
func (statusPage) Actions(m Model) []action {
	var acts []action
	if a, ok := m.takeoverAction(); ok {
		acts = append(acts, a)
	}
	if m.cfg.Strategy == nil {
		acts = append(acts, action{Key: "w", Label: i18n.T("tui.act.setup"), CLI: "sudo splitdpi scan", Do: func(m *Model) tea.Cmd {
			m.page, m.inPage = pgScan, true
			return m.openWizard()
		}})
	}
	switch {
	case m.bypassOn():
		acts = append(acts, m.stopAction())
	case m.canStart():
		acts = append(acts, m.runAction())
		if m.runMode() != config.RunBackground { // else r already does it
			acts = append(acts, action{Key: "b", Label: i18n.T("background_tui.act"), CLI: runModeCLI(config.RunBackground), Do: (*Model).runBackground})
		}
	}
	return append(acts, action{Key: "p", Label: i18n.T("tui.act.probe"), CLI: "sudo splitdpi probe", Do: (*Model).probeApps})
}

func (m Model) stopAction() action {
	return action{Key: "s", Label: i18n.T("tui.act.stop"), CLI: "sudo splitdpi stop", Do: (*Model).stop}
}

// runAction is r: it starts the bypass the way the run mode in Settings says.
func (m Model) runAction() action {
	mode := m.runMode()
	a := action{Key: "r", Label: i18n.T("tui.act.run"), CLI: runModeCLI(mode), Do: (*Model).runNow}
	switch mode {
	case config.RunBackground:
		a.Label, a.Do = i18n.T("run_mode.run_background"), (*Model).runBackground
	case config.RunBoot:
		a.Label, a.Do = i18n.T("run_mode.run_boot"), (*Model).confirmEnable
	}
	return a
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
		return m, m.refresh(), true
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
	finished := make(chan struct{})
	m.running, m.runCancel, m.runFinished, m.note = true, cancel, finished, ""
	b := m.b
	return func() tea.Msg {
		defer close(finished)
		return runDoneMsg{b.Run(ctx, false)}
	}
}

// runBackground starts the bypass as its own process, like `splitdpi run
// --background`: it outlives the TUI and its terminal, until reboot.
func (m *Model) runBackground() tea.Cmd {
	if m.bgStarting {
		return nil
	}
	m.bgStarting, m.note = true, i18n.T("background_tui.starting")
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

func (m *Model) stop() tea.Cmd {
	if m.running {
		m.runCancel() // runDoneMsg follows once the rules are gone
		return nil
	}
	b := m.b
	return func() tea.Msg {
		if err := b.Stop(); err != nil {
			return probeMsg{err: err}
		}
		st, cfg, err := b.Status()
		return statusMsg{st, cfg, err}
	}
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
// have no address to test; it names up to three blocked ones, apps first,
// and says what to do about the first.
func (m Model) probeSummary(w int) []string {
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
	lines := []string{
		row(w, i18n.T("tui.probe"), m.reachBar([3]int{open, len(blocked), untested}, min(value, 40))),
		row(w, "", joinWrapped(parts, mutedStyle.Render(" "+m.g.sep+" "), value)),
	}
	shown := blocked[:min(len(blocked), 3)]
	for _, b := range shown {
		lines = append(lines, row(w, "", badStyle.Render(m.g.fail)+" "+b.name+"  "+badStyle.Render(i18n.T("probe_short."+string(b.cat)))))
	}
	more := i18n.T("probe_summary.all")
	if n := len(blocked) - len(shown); n > 0 {
		more = i18n.T("probe_summary.more", n)
	}
	lines = append(lines, row(w, "", mutedStyle.Render(more)))
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

func (statusPage) View(m Model, w, _ int) string {
	lines := m.takeoverView(w)
	state := i18n.Enum("state", m.st.State)
	switch {
	case m.running:
		state = goodStyle.Render(i18n.T("tui.running_here"))
	case m.st.State == "running":
		proc := i18n.T("tui.process", i18n.Enum("mode", m.st.Mode), m.st.PID)
		if m.st.Mode == "background" {
			proc += ", " + i18n.T("background_tui.until_reboot")
		}
		state = goodStyle.Render(state) + mutedStyle.Render("  "+proc)
	case m.st.State == "failed":
		state = badStyle.Render(state)
	default:
		state = mutedStyle.Render(state)
	}
	lines = append(lines, row(w, i18n.T("tui.state"), state))

	if s := m.cfg.Strategy; s != nil {
		lines = append(lines, row(w, i18n.T("tui.strategy"), s.ID+mutedStyle.Render("  "+s.Source)))
	} else {
		lines = append(lines, row(w, i18n.T("tui.strategy"), warnStyle.Render(i18n.T("tui.strategy_none"))))
	}
	if m.st.Engine != "" {
		lines = append(lines, row(w, i18n.T("tui.engine"), "zapret2 "+m.st.Engine))
	}
	apps := i18n.T("tui.none")
	if len(m.cfg.Apps) > 0 {
		apps = strings.Join(m.cfg.Apps, ", ")
	}
	lines = append(lines, row(w, i18n.T("tui.apps"), apps))
	if m.cfg.DNSOn() {
		lines = append(lines, row(w, i18n.T("tui.dns"), warnStyle.Render(i18n.T("tui.dns_on"))))
	}
	lines = append(lines, m.probeSummary(w)...)
	if m.st.LastError != "" {
		lines = append(lines, row(w, i18n.T("tui.last_error"), badStyle.Render(m.st.LastError)))
	}
	if m.stErr != nil {
		lines = append(lines, badStyle.Render(m.stErr.Error()))
	}
	if m.canStart() {
		k := "b"
		if m.runMode() == config.RunBackground {
			k = "r"
		}
		lines = append(lines, "", lipgloss.NewStyle().Width(w).Render(hint(k, i18n.T("background_tui.hint"))))
	}
	return strings.Join(lines, "\n")
}
