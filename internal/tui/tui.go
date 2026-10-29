// Package tui is the terminal UI. It is a thin layer over ops:
// every action is an ops call, and the footer shows the CLI command that
// does the same thing.
package tui

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/KadirBerkpolat1/SplitDPI/internal/config"
	"github.com/KadirBerkpolat1/SplitDPI/internal/doctor"
	"github.com/KadirBerkpolat1/SplitDPI/internal/i18n"
	"github.com/KadirBerkpolat1/SplitDPI/internal/ops"
	"github.com/KadirBerkpolat1/SplitDPI/internal/presets"
	"github.com/KadirBerkpolat1/SplitDPI/internal/probe"
	"github.com/KadirBerkpolat1/SplitDPI/internal/render"
	"github.com/KadirBerkpolat1/SplitDPI/internal/scan"
	"github.com/KadirBerkpolat1/SplitDPI/internal/supervisor"
)

// Backend is the part of ops the TUI uses; *ops.Ops implements it.
type Backend interface {
	Status() (supervisor.Status, config.Config, error)
	Run(ctx context.Context, service bool) error
	Stop() error
	Logs(ctx context.Context, follow bool, w io.Writer) error
	Probe(ctx context.Context, apps []string) (map[string][]probe.Result, error)
	Doctor(ctx context.Context) []doctor.Check
	IPInfoHost() (string, error)
	Detect(ctx context.Context, asn int, country string) (scan.ASNInfo, []presets.Candidate, error)
	PresetsList() presets.Set
	AppsEnable(id string) error
	AppsDisable(id string) error
	ExcludeList() ([]string, error)
	ExcludeAdd(d string) (string, error)
	SitesList() ([]string, error)
	SitesAdd(d string) (string, error)
	SitesRemove(d string) error
	StrategyList() ([]ops.StrategyChoice, *config.Strategy, error)
	StrategySet(id string) (ops.StrategyChoice, bool, error)
	Scan(ctx context.Context, opt scan.Options, progress func(string)) (scan.Report, error)
	HasIPv6(ctx context.Context) bool
	Enable(ctx context.Context) error
	AcceptDisclaimer() error
}

type screen int

const (
	scrStatus screen = iota
	scrWizard
	scrApps
	scrLogs
	scrStrategy
)

// action is one thing the user can do on a screen. CLI is the command that
// does the same outside the TUI; the footer shows it for the focused action.
type action struct {
	Key, Label, CLI string
	Do              func(m *Model) tea.Cmd
}

const statusEvery = 2 * time.Second

type (
	tickMsg    struct{}
	logTickMsg struct{}
	logEndMsg  struct{}
	statusMsg  struct {
		st  supervisor.Status
		cfg config.Config
		err error
	}
	runDoneMsg struct{ err error }
	probeMsg   struct {
		res map[string][]probe.Result
		err error
	}
)

type Model struct {
	ctx    context.Context
	b      Backend
	scr    screen
	focus  int
	width  int
	height int
	every  func(time.Duration, func(time.Time) tea.Msg) tea.Cmd

	st    supervisor.Status
	cfg   config.Config
	stErr error
	note  string // result of the last action
	probe map[string][]probe.Result

	logs      *ring
	logCancel context.CancelFunc

	wiz   wizard
	apps  appsScreen
	strat strategyScreen

	// The bypass this TUI runs itself ("Run now"): quitting stops it.
	running     bool
	runCancel   context.CancelFunc
	runFinished chan struct{}
	confirmQuit bool
	quitting    bool
}

func New(ctx context.Context, b Backend) Model {
	m := Model{ctx: ctx, b: b, every: tea.Tick, logs: &ring{max: 500}, width: 80, height: 24}
	m.st, m.cfg, m.stErr = b.Status()
	return m
}

// Run opens the TUI on the terminal. The bypass it may run is stopped (and
// its rules removed) before Run returns.
func Run(ctx context.Context, o *ops.Ops) error {
	m := New(ctx, o)
	o.Log = m.logs // supervisor output must not scribble over the screen
	final, err := tea.NewProgram(m, tea.WithAltScreen(), tea.WithContext(ctx)).Run()
	if fm, ok := final.(Model); ok && fm.runFinished != nil {
		fm.runCancel()
		<-fm.runFinished
	}
	return err
}

func (m Model) Init() tea.Cmd { return m.tick() }

func (m Model) tick() tea.Cmd {
	return m.every(statusEvery, func(time.Time) tea.Msg { return tickMsg{} })
}

func (m Model) refresh() tea.Cmd {
	return func() tea.Msg {
		st, cfg, err := m.b.Status()
		return statusMsg{st, cfg, err}
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if next, cmd, ok := m.wizardUpdate(msg); ok {
		return next, cmd
	}
	if next, cmd, ok := m.appsUpdate(msg); ok {
		return next, cmd
	}
	if next, cmd, ok := m.strategyUpdate(msg); ok {
		return next, cmd
	}
	switch msg := msg.(type) {
	case disclaimerMsg:
		if msg.err != nil {
			m.note = errText(msg.err)
		}
		return m, m.refresh()
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case tickMsg:
		return m, tea.Batch(m.refresh(), m.tick())
	case statusMsg:
		m.st, m.cfg, m.stErr = msg.st, msg.cfg, msg.err
		return m, nil
	case runDoneMsg:
		m.running, m.runCancel = false, nil
		if msg.err != nil {
			m.note = errText(msg.err)
		}
		if m.quitting {
			return m, tea.Quit
		}
		return m, m.refresh()
	case probeMsg:
		m.probe = msg.res
		if msg.err != nil {
			m.note = errText(msg.err)
		}
		return m, nil
	case logTickMsg:
		if m.scr == scrLogs {
			return m, m.every(500*time.Millisecond, func(time.Time) tea.Msg { return logTickMsg{} })
		}
		return m, nil
	case logEndMsg:
		return m, nil
	case tea.KeyMsg:
		return m.key(msg)
	}
	return m, nil
}

func (m Model) key(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	s := k.String()
	if s == " " {
		s = "space"
	}
	if m.confirmQuit {
		switch s {
		case "y", "enter":
			m.confirmQuit, m.quitting = false, true
			m.runCancel()
			return m, nil
		case "n", "esc":
			m.confirmQuit = false
		}
		return m, nil
	}
	if s == "ctrl+c" {
		cmd := m.quit()
		return m, cmd
	}
	if !m.showDisclaimer() {
		var next Model
		var cmd tea.Cmd
		handled := false
		switch m.scr {
		case scrWizard:
			next, cmd, handled = m.wizardKey(k, s)
		case scrApps:
			next, cmd, handled = m.appsKey(k)
		case scrStrategy:
			next, cmd, handled = m.strategyKey(s)
		}
		if handled {
			return next, cmd
		}
		if s == "q" {
			cmd := m.quit()
			return m, cmd
		}
		if s == "esc" && m.scr != scrStatus {
			return m.leave(), nil
		}
	}
	acts := m.actions()
	switch s {
	case "tab", "right":
		m.focus = (m.focus + 1) % len(acts)
		return m, nil
	case "shift+tab", "left":
		m.focus = (m.focus + len(acts) - 1) % len(acts)
		return m, nil
	}
	for i, a := range acts {
		if a.Key == s {
			if s != "enter" {
				m.focus = i
			}
			cmd := a.Do(&m)
			return m, cmd
		}
	}
	if s == "enter" && m.focus < len(acts) {
		cmd := acts[m.focus].Do(&m)
		return m, cmd
	}
	return m, nil
}

// quit leaves the TUI, but first asks when it runs the bypass itself.
func (m *Model) quit() tea.Cmd {
	if m.running {
		m.confirmQuit = true
		return nil
	}
	*m = m.leave()
	return tea.Quit
}

// leave goes back to the status screen and stops what the old screen ran.
func (m Model) leave() Model {
	if m.logCancel != nil {
		m.logCancel()
		m.logCancel = nil
	}
	if m.wiz.scanning {
		m.wiz.cancelled = true
		m.wiz.scanCancel()
	}
	m.apps.typing = false
	m.scr, m.focus = scrStatus, 0
	return m
}

func (m Model) actions() []action {
	if m.showDisclaimer() {
		return disclaimerActions()
	}
	switch m.scr {
	case scrLogs:
		return logsActions()
	case scrWizard:
		return m.wizardActions()
	case scrApps:
		return m.appsActions()
	case scrStrategy:
		return m.strategyActions()
	}
	return m.statusActions()
}

func (m Model) View() string {
	var body string
	switch {
	case m.showDisclaimer():
		body = m.disclaimerView()
	case m.scr == scrLogs:
		body = m.logsView()
	case m.scr == scrWizard:
		body = m.wizardView()
	case m.scr == scrApps:
		body = m.appsView()
	case m.scr == scrStrategy:
		body = m.strategyView()
	default:
		body = m.statusView()
	}
	// Width wraps long prose (questions, errors) instead of cutting it off.
	return lipgloss.NewStyle().Width(m.width).MaxWidth(m.width).MaxHeight(m.height).Render(body + "\n" + m.footer())
}

// errText words an ops error for the note line, as the CLI does.
func errText(err error) string {
	var conflict ops.ConflictError
	switch {
	case errors.As(err, &conflict):
		msgs := []string{i18n.T("run.conflict")}
		for _, c := range conflict.Checks {
			msgs = append(msgs, c.Msg)
		}
		return strings.Join(msgs, " ")
	case errors.Is(err, render.ErrNoStrategy):
		return i18n.T("run.no_strategy")
	case errors.Is(err, ops.ErrEngineMissing):
		return i18n.T("engine.not_installed")
	}
	return err.Error()
}

// ring keeps the last max lines written to it.
type ring struct {
	mu      sync.Mutex
	max     int
	lines   []string
	partial string
}

func (r *ring) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.partial + string(p)
	parts := strings.Split(s, "\n")
	r.partial = parts[len(parts)-1]
	r.lines = append(r.lines, parts[:len(parts)-1]...)
	if over := len(r.lines) - r.max; over > 0 {
		r.lines = r.lines[over:]
	}
	return len(p), nil
}

// Last returns up to n of the newest complete lines.
func (r *ring) Last(n int) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if n > len(r.lines) {
		n = len(r.lines)
	}
	return append([]string(nil), r.lines[len(r.lines)-n:]...)
}

func (r *ring) String() string { return strings.Join(r.Last(r.max), "\n") }
