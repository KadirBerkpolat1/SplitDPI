// Package tui is the terminal UI. It is a thin layer over ops:
// every action is an ops call, and the footer shows the CLI command that
// does the same thing.
package tui

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/KadirBerkpolat1/SplitDPI/internal/config"
	"github.com/KadirBerkpolat1/SplitDPI/internal/dns"
	"github.com/KadirBerkpolat1/SplitDPI/internal/doctor"
	"github.com/KadirBerkpolat1/SplitDPI/internal/engine"
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
	SetLang(lang string) error

	SitesImport(path string) ([]string, []ops.ImportSkip, error)
	DetectForeign(ctx context.Context) ([]ops.Foreign, error)
	Takeover(ctx context.Context, f ops.Foreign) (ops.TakeoverReport, error)

	DNSProviders() []ops.DNSProvider
	SetDNS(provider string, proto dns.Protocol) (running bool, err error)
	UninstallPlan() ops.UninstallPlan
	Uninstall(ctx context.Context) (ops.UninstallReport, error)

	EngineInstall(ctx context.Context, from string) (engine.Engine, error)
}

const statusEvery = 2 * time.Second

type (
	tickMsg   struct{}
	statusMsg struct {
		st  supervisor.Status
		cfg config.Config
		err error
	}
)

type Model struct {
	ctx     context.Context
	b       Backend
	version string
	g       glyphs
	width   int
	height  int
	every   func(time.Duration, func(time.Time) tea.Msg) tea.Cmd

	page   int  // the selected menu entry
	inPage bool // keys go to the page instead of the menu
	focus  int  // the page action the footer highlights
	help   bool
	dialog *confirm
	ask    *dnsAsk // the scan's encrypted DNS question

	st    supervisor.Status
	cfg   config.Config
	stErr error
	note  string // result of the last action
	probe map[string][]probe.Result

	foreign   []ops.Foreign // other zapret installs that get in the way
	takenOver []string      // what the last takeover did

	logs       *ring
	logCancel  context.CancelFunc
	logTicking bool

	wiz      wizard
	apps     appsScreen
	strat    strategyScreen
	settings settingsScreen

	// The bypass this TUI runs itself ("Run now"): quitting stops it.
	running     bool
	runCancel   context.CancelFunc
	runFinished chan struct{}
	confirmQuit bool
	quitting    bool
}

func New(ctx context.Context, b Backend, version string) Model {
	m := Model{ctx: ctx, b: b, version: version, g: glyphsFor(os.Getenv), every: tea.Tick, logs: &ring{max: 500}, width: 80, height: 24}
	m.st, m.cfg, m.stErr = b.Status()
	return m
}

// Run opens the TUI on the terminal. The bypass it may run is stopped (and
// its rules removed) before Run returns.
func Run(ctx context.Context, o *ops.Ops, version string) error {
	m := New(ctx, o, version)
	o.Log = m.logs // supervisor output must not scribble over the screen
	final, err := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion(), tea.WithContext(ctx)).Run()
	if fm, ok := final.(Model); ok {
		fm.stopRun()
	}
	return err
}

// stopRun stops the bypass this TUI still runs and waits until its rules are gone.
func (m Model) stopRun() {
	if m.runCancel != nil {
		m.runCancel()
		<-m.runFinished
	}
}

func (m Model) Init() tea.Cmd { return tea.Batch(m.tick(), m.detectForeign()) }

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
	for _, p := range pages {
		if next, cmd, ok := p.Update(m, msg); ok {
			return next, cmd
		}
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
	case tea.MouseMsg:
		return m.mouse(msg)
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
	switch {
	case m.settings.uninstalled:
		// The program is gone: any key leaves.
		m.quitting = true
		m.stopAll()
		if m.running {
			m.runCancel() // runDoneMsg quits
			return m, nil
		}
		return m, tea.Quit
	case m.confirmQuit:
		switch s {
		case "y", "enter":
			m.confirmQuit, m.quitting = false, true
			m.stopAll()
			m.runCancel()
		case "n", "esc":
			m.confirmQuit = false
		}
		return m, nil
	case s == "ctrl+c":
		cmd := m.quit()
		return m, cmd
	case m.dialog != nil:
		return m.dialogKey(s)
	case m.ask != nil:
		return m.dnsAskKey(s)
	case m.help:
		if s == "esc" || s == "?" {
			m.help = false
		}
		return m, nil
	case m.showDisclaimer():
		return m.do(disclaimerActions(), s)
	case m.tooSmall():
		if s == "q" {
			cmd := m.quit()
			return m, cmd
		}
		return m, nil
	}
	p := pages[m.page]
	if m.inPage {
		if next, cmd, ok := p.Key(m, k, s); ok {
			return next, cmd
		}
	}
	switch s {
	case "q":
		cmd := m.quit()
		return m, cmd
	case "?":
		m.help = true
		return m, nil
	case "esc":
		if m.inPage {
			m.inPage = false
			return m, nil
		}
	case "up", "k", "down", "j":
		if !m.inPage {
			next := m.page + 1
			if s == "up" || s == "k" {
				next = m.page - 1
			}
			cmd := m.selectPage(next)
			return m, cmd
		}
		return m, nil
	case "tab", "enter", "right":
		if !m.inPage {
			m.inPage = true
			return m, nil
		}
	}
	return m.do(p.Actions(m), s)
}

// do runs the action bound to s. In a page, tab and the arrows move the
// footer focus and enter runs the focused action.
func (m Model) do(acts []action, s string) (tea.Model, tea.Cmd) {
	if n := len(acts); m.inPage && n > 0 {
		switch s {
		case "tab", "right":
			m.focus = (m.focus + 1) % n
			return m, nil
		case "shift+tab", "left":
			m.focus = (m.focus + n - 1) % n
			return m, nil
		}
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
	if s == "enter" && m.focus < len(acts) && acts[m.focus].Do != nil {
		cmd := acts[m.focus].Do(&m)
		return m, cmd
	}
	return m, nil
}

// selectPage moves the menu to page i, if there is one.
func (m *Model) selectPage(i int) tea.Cmd {
	if i < 0 || i >= len(pages) || i == m.page {
		return nil
	}
	m.page, m.focus, m.note = i, 0, ""
	return pages[i].Open(m)
}

// mouse selects the menu entry under a left click.
func (m Model) mouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft ||
		m.confirmQuit || m.dialog != nil || m.ask != nil || m.help || m.showDisclaimer() || m.tooSmall() || m.settings.uninstalled {
		return m, nil
	}
	i := msg.Y - menuTop
	if msg.X >= menuWidth || i < 0 || i >= len(pages) {
		return m, nil
	}
	m.inPage = false
	cmd := m.selectPage(i)
	return m, cmd
}

// quit leaves the TUI, but first asks when it runs the bypass itself.
func (m *Model) quit() tea.Cmd {
	if m.running {
		m.confirmQuit = true
		return nil
	}
	m.stopAll()
	return tea.Quit
}

// stopAll stops the log stream and a running scan.
func (m *Model) stopAll() {
	if m.logCancel != nil {
		m.logCancel()
		m.logCancel = nil
	}
	if m.wiz.scanning {
		m.wiz.cancelled = true
		m.wiz.scanCancel()
	}
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
