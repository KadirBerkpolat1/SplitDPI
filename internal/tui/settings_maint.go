package tui

import (
	"fmt"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/KadirBerkpolat1/SplitDPI/internal/doctor"
	"github.com/KadirBerkpolat1/SplitDPI/internal/engine"
	"github.com/KadirBerkpolat1/SplitDPI/internal/i18n"
	"github.com/KadirBerkpolat1/SplitDPI/internal/ops"
)

// The Maintenance rows: refresh the presets, install the engine, check the
// system, switch from or back to zapret. A row at work says so in its value;
// what it did shows under the list.

type (
	presetsMsg struct {
		version string
		apps    int
		err     error
	}
	settingsEngineMsg struct {
		eng engine.Engine
		err error
	}
	settingsDoctorMsg struct{ checks []doctor.Check }
	revertMsg         struct{ r ops.RevertReport }
)

func working() string { return mutedStyle.Render(i18n.T("settings_page.working")) }

// withRestart adds that a running bypass picks up the change when it restarts.
func (m Model) withRestart(lines []string) []string {
	if m.bypassOn() {
		lines = append(lines, i18n.T("settings_dns.on_restart"))
	}
	return lines
}

func presetsValue(m Model) string {
	switch {
	case m.settings.busy == "presets":
		return working()
	case m.settings.presets == "":
		return mutedStyle.Render(i18n.T("tui.none"))
	}
	return i18n.T("settings_presets.version", m.settings.presets)
}

func presetsActions(Model) []action {
	return []action{{Key: "enter", Label: i18n.T("settings_presets.do"), CLI: "sudo splitdpi presets update", Do: (*Model).updatePresets}}
}

func (m *Model) updatePresets() tea.Cmd {
	if m.settings.busy != "" {
		return nil
	}
	m.settings.busy, m.settings.result = "presets", nil
	b, ctx := m.b, m.ctx
	return func() tea.Msg {
		v, n, err := b.PresetsUpdate(ctx)
		return presetsMsg{v, n, err}
	}
}

func engineVersion(e engine.Engine) string {
	return strings.TrimPrefix(filepath.Base(e.Dir), "zapret2-")
}

func engineValue(m Model) string {
	s := m.settings
	switch {
	case s.busy == "engine":
		return working()
	case s.engine == "":
		return warnStyle.Render(i18n.T("settings_engine.missing"))
	case s.engine != engine.Version:
		return warnStyle.Render(i18n.T("settings_engine.old", s.engine, engine.Version))
	}
	return "zapret2 " + s.engine
}

func engineActions(m Model) []action {
	key := "settings_engine.reinstall"
	switch m.settings.engine {
	case "":
		key = "settings_engine.install"
	case engine.Version:
	default:
		key = "settings_engine.update"
	}
	return []action{{Key: "enter", Label: i18n.T(key), CLI: "sudo splitdpi engine install", Do: (*Model).settingsInstallEngine}}
}

// settingsInstallEngine downloads and installs the pinned engine, without a
// confirm, as the setup does.
func (m *Model) settingsInstallEngine() tea.Cmd {
	if m.settings.busy != "" {
		return nil
	}
	m.settings.busy, m.settings.result = "engine", nil
	b, ctx, logs := m.b, m.ctx, m.logs
	return func() tea.Msg {
		fmt.Fprintln(logs, i18n.T("engine.downloading", engine.URL))
		e, err := b.EngineInstall(ctx, "")
		return settingsEngineMsg{e, err}
	}
}

func doctorValue(m Model) string {
	s := m.settings
	if s.checking {
		return working()
	}
	if s.checks == nil {
		return mutedStyle.Render(i18n.T("settings_doctor.not_run"))
	}
	warns, blocks := 0, 0
	for _, c := range s.checks {
		switch c.Severity {
		case doctor.Warn:
			warns++
		case doctor.Blocker:
			blocks++
		}
	}
	switch {
	case blocks > 0:
		return badStyle.Render(i18n.N("settings_doctor.blockers", blocks))
	case warns > 0:
		return warnStyle.Render(i18n.N("settings_doctor.warnings", warns))
	}
	return goodStyle.Render(i18n.T("settings_doctor.ok"))
}

func doctorActions(Model) []action {
	return []action{{Key: "enter", Label: i18n.T("settings_doctor.do"), CLI: "sudo splitdpi doctor", Do: func(m *Model) tea.Cmd {
		m.settings.doctorOpen, m.focus = true, 0
		return m.settingsDoctor()
	}}}
}

func (m *Model) settingsDoctor() tea.Cmd {
	if m.settings.checking {
		return nil
	}
	m.settings.checking = true
	b, ctx := m.b, m.ctx
	return func() tea.Msg { return settingsDoctorMsg{b.Doctor(ctx)} }
}

func (m Model) doctorViewActions() []action {
	return []action{
		{Key: "r", Label: i18n.T("settings_doctor.again"), CLI: "sudo splitdpi doctor", Do: (*Model).settingsDoctor},
		{Key: "esc", Label: i18n.T("tui.act.back"), Do: func(m *Model) tea.Cmd { m.settings.doctorOpen = false; return nil }},
	}
}

// doctorViewKey leaves r to the footer; other keys do nothing here.
func (m Model) doctorViewKey(s string) (Model, tea.Cmd, bool) {
	switch s {
	case "esc":
		m.settings.doctorOpen = false
		return m, nil, true
	case "r", "enter", "tab", "shift+tab", "left", "right":
		return m, nil, false
	}
	return m, nil, true
}

// doctorView lists every check with its fix, problems first.
func (m Model) doctorView(w, h int) string {
	s := m.settings
	lines := strings.Split(lipgloss.NewStyle().Width(w).Render(i18n.T("settings_doctor.head")), "\n")
	lines = append(lines, "")
	if s.checking {
		lines = append(lines, working())
		return strings.Join(lines, "\n")
	}
	item := lipgloss.NewStyle().Width(w - 2)
	for _, sev := range []doctor.Severity{doctor.Blocker, doctor.Warn, doctor.OK} {
		for _, c := range s.checks {
			if c.Severity != sev {
				continue
			}
			mark := goodStyle.Render(m.g.on)
			switch sev {
			case doctor.Warn:
				mark = warnStyle.Render("!")
			case doctor.Blocker:
				mark = badStyle.Render(m.g.fail)
			}
			for i, l := range strings.Split(item.Render(c.Msg), "\n") {
				if i == 0 {
					lines = append(lines, mark+strings.Repeat(" ", max(2-lipgloss.Width(mark), 1))+l)
				} else {
					lines = append(lines, "  "+l)
				}
			}
			if c.Fix != "" {
				lines = append(lines, "    "+mutedStyle.Render("$ ")+commandStyle.Render(c.Fix))
			}
		}
	}
	if len(s.checks) == 0 {
		lines = append(lines, goodStyle.Render(i18n.T("settings_doctor.none_found")))
	}
	return strings.Join(scroll(lines, 0, h, w), "\n")
}

func canTakeOverAny(m Model) bool {
	_, ok := m.takeoverAction()
	return ok
}

func takeoverValue(m Model) string {
	var parts []string
	for _, f := range m.foreign {
		switch {
		case !canTakeOver(f):
		case f.Running:
			parts = append(parts, i18n.T("settings_takeover.running", f.Kind))
		default:
			parts = append(parts, i18n.T("settings_takeover.boot", f.Kind))
		}
	}
	return warnStyle.Render(strings.Join(parts, ", "))
}

// takeoverActions are Status's z, on enter.
func takeoverActions(m Model) []action {
	a, _ := m.takeoverAction()
	a.Key = "enter"
	return []action{a}
}

func hasTakenOver(m Model) bool { return len(m.settings.takenOver) > 0 }

func (m *Model) loadTakenOver() {
	list, err := m.b.TakenOver()
	if err != nil {
		fmt.Fprintln(m.logs, err)
	}
	m.settings.takenOver = list
}

func revertValue(m Model) string { return mutedStyle.Render(strings.Join(m.settings.takenOver, ", ")) }

func revertActions(Model) []action {
	return []action{{Key: "enter", Label: i18n.T("settings_revert.do"), CLI: "sudo splitdpi revert", Do: (*Model).confirmRevert}}
}

// confirmRevert says that SplitDPI stops and what is turned back on.
func (m *Model) confirmRevert() tea.Cmd {
	changes := []string{i18n.T("settings_revert.stop")}
	for _, s := range m.settings.takenOver {
		changes = append(changes, i18n.T("settings_revert.restore", s))
	}
	m.dialog = &confirm{
		title:   i18n.T("settings_revert.title"),
		changes: append(changes, i18n.T("settings_revert.kept")),
		undo:    "sudo splitdpi takeover --yes",
		yes:     (*Model).revert,
	}
	return nil
}

// revert stops the bypass this TUI runs, if any, then undoes the takeover.
func (m *Model) revert() tea.Cmd {
	m.note, m.settings.result = i18n.T("settings_revert.working"), nil
	b, ctx, w, stop := m.b, m.ctx, m.logs, m.stopOwnRun()
	return func() tea.Msg {
		stop()
		r := b.Revert(ctx, false, false)
		for _, l := range r.Leftover {
			fmt.Fprintln(w, i18n.T("revert.leftover", l))
		}
		return revertMsg{r}
	}
}

func (m Model) maintUpdate(msg tea.Msg) (Model, tea.Cmd, bool) {
	s := &m.settings
	switch msg := msg.(type) {
	case presetsMsg:
		s.busy = ""
		if msg.err != nil {
			fmt.Fprintln(m.logs, msg.err)
			s.result = []string{badStyle.Render(errText(msg.err))}
			return m, nil, true
		}
		s.presets = msg.version
		s.result = m.withRestart([]string{goodStyle.Render(i18n.T("presets.update.done", msg.version, msg.apps))})
		return m, nil, true
	case settingsEngineMsg:
		s.busy = ""
		if msg.err != nil {
			fmt.Fprintln(m.logs, msg.err)
			s.result = []string{badStyle.Render(installErrText(msg.err)), i18n.T("engine.download_hint", engine.URL)}
			return m, nil, true
		}
		s.engine = engineVersion(msg.eng)
		s.result = m.withRestart([]string{goodStyle.Render(i18n.T("engine.installed", engine.Version, msg.eng.Dir))})
		return m, m.refresh(), true
	case settingsDoctorMsg:
		s.checking, s.checks = false, msg.checks
		if s.checks == nil {
			s.checks = []doctor.Check{}
		}
		return m, nil, true
	case revertMsg:
		s.result = nil
		for _, d := range msg.r.Removed {
			s.result = append(s.result, i18n.T("revert.removed", d))
		}
		for _, d := range msg.r.Restored {
			s.result = append(s.result, i18n.T("takeover.restored", d))
		}
		for _, d := range msg.r.Leftover {
			s.result = append(s.result, warnStyle.Render(i18n.T("revert.leftover", d)))
		}
		m.note = i18n.T("settings_revert.done")
		if len(msg.r.Leftover) > 0 {
			m.note = i18n.T("revert.not_clean")
		}
		m.loadTakenOver()
		return m, batch(m.refresh(), m.detectForeign()), true
	}
	return m, nil, false
}
