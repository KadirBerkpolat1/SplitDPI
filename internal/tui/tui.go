// Package tui is the terminal UI. It is a thin layer over ops: every
// action is an ops call, and the help lists the CLI command that does the
// same thing.
package tui

import (
	"context"
	"errors"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/KadirBerkpolat1/SplitDPI/internal/config"
	"github.com/KadirBerkpolat1/SplitDPI/internal/dns"
	"github.com/KadirBerkpolat1/SplitDPI/internal/doctor"
	"github.com/KadirBerkpolat1/SplitDPI/internal/engine"
	"github.com/KadirBerkpolat1/SplitDPI/internal/i18n"
	"github.com/KadirBerkpolat1/SplitDPI/internal/initsys"
	"github.com/KadirBerkpolat1/SplitDPI/internal/ops"
	"github.com/KadirBerkpolat1/SplitDPI/internal/presets"
	"github.com/KadirBerkpolat1/SplitDPI/internal/probe"
	"github.com/KadirBerkpolat1/SplitDPI/internal/render"
	"github.com/KadirBerkpolat1/SplitDPI/internal/scan"
	"github.com/KadirBerkpolat1/SplitDPI/internal/supervisor"
	"github.com/KadirBerkpolat1/SplitDPI/internal/sysinfo"
)

// Backend is the part of ops the TUI uses; *ops.Ops implements it.
type Backend interface {
	Status() (supervisor.Status, config.Config, error)
	Run(ctx context.Context, service bool) error
	Background(ctx context.Context) (int, error)
	Stop() error
	RestartCommand() string
	Logs(ctx context.Context, follow bool, w io.Writer) error
	Probe(ctx context.Context, apps []string) (map[string][]probe.Result, error)
	Doctor(ctx context.Context) []doctor.Check
	IPInfoHost() (string, error)
	Detect(ctx context.Context, asn int, country string) (scan.ASNInfo, []presets.Candidate, error)
	PresetsList() presets.Set
	AppsEnable(id string) error
	AppsDisable(id string) error
	AppNew(id, name string) error
	AppHostAdd(id, d string) (string, error)
	AppHostRemove(id, d string) error
	AppReset(id string) error
	ExcludeList() ([]string, error)
	ExcludeAdd(d string) (string, error)
	ExcludeRemove(d string) error
	SitesList() ([]string, error)
	SitesAdd(d string) (string, error)
	SitesRemove(d string) error
	StrategyList() ([]ops.StrategyChoice, *config.Strategy, error)
	StrategySet(id string) (ops.StrategyChoice, bool, error)
	Scan(ctx context.Context, opt scan.Options, progress func(string)) (scan.Report, error)
	HasIPv6(ctx context.Context) bool
	Enable(ctx context.Context, stopHere func()) (ops.EnableReport, error)
	Disable(ctx context.Context) error
	ServiceState() ops.ServiceState
	AcceptDisclaimer() error
	SetLang(lang string) error
	SetRunMode(mode config.RunMode) error
	SetMode(mode config.Mode) error
	SetSkipIfaces(list []string) error
	SetQNum(n int) error
	PresetsUpdate(ctx context.Context) (version string, apps int, err error)

	SitesImport(path string) ([]string, []ops.ImportSkip, error)
	DetectForeign(ctx context.Context) ([]ops.Foreign, error)
	Takeover(ctx context.Context, f ops.Foreign) (ops.TakeoverReport, error)
	TakenOver() ([]string, error)
	Revert(ctx context.Context, rulesOnly, purge bool) ops.RevertReport

	DNSProviders() []ops.DNSProvider
	SetDNS(provider string, proto dns.Protocol) (running bool, err error)
	SystemEncryptedDNS(ctx context.Context) (name string, ok bool)
	UninstallPlan() ops.UninstallPlan
	Uninstall(ctx context.Context) (ops.UninstallReport, error)

	EngineInstall(ctx context.Context, from string) (engine.Engine, error)
	EngineStatus() (engine.Engine, bool)

	MissingPackages(tools ...sysinfo.Tool) sysinfo.Packages
	InstallPackages(ctx context.Context, p sysinfo.Packages) error
}

const statusEvery = 2 * time.Second

type (
	tickMsg   struct{}
	statusMsg struct {
		st  supervisor.Status
		cfg config.Config
		err error
		svc ops.ServiceState
	}
)

// readStatus is what the pages show of the bypass, the config and the service.
func readStatus(b Backend) statusMsg {
	st, cfg, err := b.Status()
	return statusMsg{st, cfg, err, b.ServiceState()}
}

type Model struct {
	ctx     context.Context
	b       Backend
	version string
	g       glyphs
	width   int
	height  int
	every   func(time.Duration, func(time.Time) tea.Msg) tea.Cmd
	out     io.Writer // the program's output, for escape sequences the renderer cannot send

	page    int // the selected tab
	help    bool
	helpTop int // how far the help is scrolled
	dialog  *confirm
	ask     *dnsAsk // the scan's encrypted DNS question
	details bool    // Status shows the technical details

	st         supervisor.Status
	cfg        config.Config
	stErr      error
	svc        ops.ServiceState
	note       string // result of the last action
	probe      map[string][]probe.Result
	probeNames map[string]string // the probed apps' names by id
	appNames   map[string]string // every app's name by id
	ispASN     int               // the strategy's ASN that ispName is for
	ispName    string            // the name of the strategy's ISP, if known

	// pending: the running bypass has not picked up a saved change yet.
	pending bool

	foreign   []ops.Foreign // other zapret installs that get in the way
	takenOver []string      // what the last takeover did

	logs       *ring
	logCancel  context.CancelFunc
	logStream  int // numbers the streams; the ring tags each stream's lines with it
	logTicking bool

	wiz      wizard
	apps     appsScreen
	strat    strategyScreen
	settings settingsScreen

	// The bypass this TUI runs itself: quitting stops it.
	running     bool
	runCancel   context.CancelFunc
	runFinished chan struct{}
	restarting  bool // the run here ends to start again
	confirmQuit bool
	quitting    bool

	// The bypass this TUI starts in the background (run --background).
	bgStarting   bool
	toBackground bool // quitting moves the bypass run here to the background
	bgPID        int  // the background run quitting left behind

	// The service change under way, if any, and the quit that waits for it.
	svcOp     *serviceOp
	quitAfter bool
	quitNote  string // what that change did, printed once the screen is back
}

func New(ctx context.Context, b Backend, version string) Model {
	m := Model{ctx: ctx, b: b, version: version, g: glyphsFor(os.Getenv), every: tea.Tick, out: os.Stdout, logs: &ring{max: 500}, width: 80, height: 24}
	s := readStatus(b)
	m.st, m.cfg, m.stErr, m.svc = s.st, s.cfg, s.err, s.svc
	m.loadNames(b.PresetsList())
	return m
}

// loadNames keeps the names Status shows: the apps' and the strategy's ISP's.
func (m *Model) loadNames(set presets.Set) {
	m.appNames = make(map[string]string, len(set.Apps))
	for id, a := range set.Apps {
		m.appNames[id] = a.Name
	}
	m.ispASN, m.ispName = 0, ""
	if s := m.cfg.Strategy; s != nil {
		m.ispASN = s.ASN
		for _, isp := range set.ISPs {
			if slices.Contains(isp.ASN, s.ASN) {
				m.ispName = isp.Name
				break
			}
		}
	}
}

// Run opens the TUI on the terminal. A service change the program ended in
// the middle of is waited for, and the bypass it may run is stopped (and its
// rules removed), before Run returns; then the share link of a strategy
// saved in this session, the background run quitting left and what the
// service change quitting waited for did are printed where the scrollback
// keeps them.
func Run(ctx context.Context, o *ops.Ops, version string) error {
	m := New(ctx, o, version)
	o.Log = m.logs // supervisor output must not scribble over the screen
	final, err := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion(), tea.WithContext(ctx), tea.WithOutput(m.out)).Run()
	if fm, ok := final.(Model); ok {
		fm.waitService(m.out)
		fm.stopRun()
		fm.printShare(m.out)
		fm.printBackground(m.out)
		fm.printQuitNote(m.out)
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
	b := m.b
	return func() tea.Msg { return readStatus(b) }
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if d, ok := msg.(serviceDoneMsg); ok {
		return m.serviceDone(d)
	}
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
		m.helpTop = min(m.helpTop, m.helpMax())
		return m, nil
	case tickMsg:
		return m, tea.Batch(m.refresh(), m.tick())
	case statusMsg:
		m.st, m.cfg, m.stErr, m.svc = msg.st, msg.cfg, msg.err, msg.svc
		if !m.bypassOn() {
			m.pending = false
		}
		if s := m.cfg.Strategy; s != nil && s.ASN != m.ispASN {
			m.loadNames(m.b.PresetsList())
		}
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
		return m.quitKey(s)
	case s == "ctrl+c":
		cmd := m.quit()
		return m, cmd
	case m.dialog != nil:
		return m.dialogKey(s)
	case m.ask != nil:
		return m.dnsAskKey(s)
	case m.help:
		switch s {
		case "esc", "?":
			m.help = false
		case "q":
			m.help = false
			cmd := m.quit()
			return m, cmd
		case "up", "k":
			m.helpTop = max(m.helpTop-1, 0)
		case "down", "j":
			m.helpTop = min(m.helpTop+1, m.helpMax())
		case "pgup":
			m.helpTop = max(m.helpTop-m.popupRoom()/2, 0)
		case "pgdown":
			m.helpTop = min(m.helpTop+m.popupRoom()/2, m.helpMax())
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
	if next, cmd, ok := p.Key(m, k, s); ok {
		return next, cmd
	}
	if !m.typing() {
		switch s {
		case "q":
			cmd := m.quit()
			return m, cmd
		case "?":
			m.help, m.helpTop = true, 0
			return m, nil
		case "tab":
			cmd := m.selectPage((m.page + 1) % len(pages))
			return m, cmd
		case "shift+tab":
			cmd := m.selectPage((m.page + len(pages) - 1) % len(pages))
			return m, cmd
		case restartKey:
			if a, ok := m.restartAction(); ok {
				cmd := a.Do(&m)
				return m, cmd
			}
			return m, nil
		}
		if n, err := strconv.Atoi(s); err == nil && n >= 1 && n <= len(pages) {
			cmd := m.selectPage(n - 1)
			return m, cmd
		}
	}
	return m.do(p.Actions(m), s)
}

// quitKey answers the quit prompt: stop and quit (q again does that too),
// move the bypass to the background or the service and quit, or stay.
func (m Model) quitKey(s string) (tea.Model, tea.Cmd) {
	switch {
	case s == "b" || isYes(s) || s == "q":
		m.confirmQuit = false
		// In run mode boot, "b" sets the service up, which stops the run
		// here only once nothing stands in the way; quitting follows.
		if s == "b" && m.quitMove() == config.RunBoot {
			m.quitAfter = true
			cmd := m.enable()
			return m, cmd
		}
		// runDoneMsg quits, or for "b" first starts the background run.
		m.quitting, m.toBackground = true, s == "b"
		m.stopAll()
		if m.runCancel == nil { // the run here ended while the prompt was open
			return m.Update(runDoneMsg{})
		}
		m.runCancel()
	case isNo(s):
		m.confirmQuit = false
	}
	return m, nil
}

// typing is whether a text input has the keys: then q, ?, Tab and the
// digits are typed, and esc leaves the input.
func (m Model) typing() bool {
	switch m.page {
	case pgSites:
		return m.apps.typing
	case pgSetup:
		return m.wiz.step == wSite
	case pgSettings:
		s := m.settings
		return s.text != nil || s.ifaces != nil && s.ifaces.adding
	}
	return false
}

// do runs the action bound to s.
func (m Model) do(acts []action, s string) (tea.Model, tea.Cmd) {
	for _, a := range acts {
		if a.Key == s && a.Do != nil {
			cmd := a.Do(&m)
			return m, cmd
		}
	}
	return m, nil
}

// selectPage switches to tab i, if there is one; the page left closes what
// it keeps running.
func (m *Model) selectPage(i int) tea.Cmd {
	if i < 0 || i >= len(pages) || i == m.page {
		return nil
	}
	pages[m.page].Close(m)
	m.page, m.note = i, ""
	return pages[i].Open(m)
}

// mouse handles a left click and the wheel. A popup takes the clicks: a
// click on one of its keys or rows presses it, a click beside the help
// closes it. Otherwise a tab opens its page, the pill starts or stops the
// bypass, and a key in the hint bar is pressed.
func (m Model) mouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if m.tooSmall() || m.settings.uninstalled || msg.Action != tea.MouseActionPress {
		return m, nil
	}
	switch msg.Button {
	case tea.MouseButtonWheelUp:
		return m.key(tea.KeyMsg{Type: tea.KeyUp})
	case tea.MouseButtonWheelDown:
		return m.key(tea.KeyMsg{Type: tea.KeyDown})
	case tea.MouseButtonLeft:
	default:
		return m, nil
	}
	hit := func(s span) bool { return msg.Y == s.y && msg.X >= s.x0 && msg.X < s.x1 }
	if spans, box, ok := m.popupSpans(); ok {
		for _, s := range spans {
			if hit(s) {
				return m.pressPopup(s.key)
			}
		}
		inside := msg.X >= box[0] && msg.X < box[2] && msg.Y >= box[1] && msg.Y < box[3]
		if m.help && !inside && !m.confirmQuit && m.dialog == nil && m.ask == nil {
			m.help = false
		}
		return m, nil
	}
	s := m.layout()
	for _, sp := range s.headSpans {
		if !hit(sp) {
			continue
		}
		if sp.key == pillKey {
			if a, ok := m.pillAction(); ok {
				cmd := a.Do(&m)
				return m, cmd
			}
			return m, nil
		}
		n, _ := strconv.Atoi(sp.key)
		cmd := m.selectPage(n - 1)
		return m, cmd
	}
	for _, sp := range s.barSpans {
		if hit(sp) {
			return m.key(keyMsg(sp.key))
		}
	}
	return m, nil
}

// pressPopup presses a popup's key, or chooses the row "#n" of its list.
func (m Model) pressPopup(key string) (tea.Model, tea.Cmd) {
	if n, ok := strings.CutPrefix(key, "#"); ok && m.ask != nil {
		a := *m.ask
		a.cursor, _ = strconv.Atoi(n)
		m.ask = &a
		return m.dnsAskKey("enter")
	}
	return m.key(keyMsg(key))
}

// keyMsg is the key press that an action's key name stands for.
func keyMsg(k string) tea.KeyMsg {
	switch k {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "space":
		return tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
}

// quit leaves the TUI, but first asks when it runs the bypass itself. It does
// not leave while packages install: that would kill the package manager. Nor
// during a service change, which cut short could leave no bypass running:
// the quit goes on once that is done.
func (m *Model) quit() tea.Cmd {
	if m.wiz.pkgsInstalling {
		m.note = i18n.T("tui.pkgs.wait")
		return nil
	}
	if m.svcOp != nil {
		m.note, m.quitAfter = i18n.T("tui.service_wait"), true
		return nil
	}
	if m.running {
		m.confirmQuit = true
		return nil
	}
	m.stopAll()
	return tea.Quit
}

// stopAll stops the log stream and a running scan.
func (m *Model) stopAll() {
	m.stopLogs()
	if m.wiz.scanning {
		m.wiz.cancelled = true
		m.wiz.scanCancel()
	}
}

// changed marks a saved change that the running bypass picks up only when
// it restarts, and returns what to tell about it: press R. A stopped bypass
// picks it up when it starts, so there is nothing to tell.
func (m *Model) changed() string {
	if !m.bypassOn() {
		return ""
	}
	m.pending = true
	return i18n.T("tui.restart_hint", restartKey)
}

// withChange is note followed by what changed says, if anything.
func (m *Model) withChange(note string) string {
	if c := m.changed(); c != "" {
		return note + " " + c
	}
	return note
}

// errText words an ops error for the note line, as the CLI does.
func errText(err error) string {
	var takeover ops.TakeoverError
	var conflict ops.ConflictError
	var already supervisor.ErrAlreadyRunning
	var notStarted ops.BackgroundNotStartedError
	var nobin ops.BinaryMissingError
	var unsafe ops.UnsafeBinaryError
	var unknownApp render.ErrUnknownApp
	var badStrategy render.ErrBadStrategy
	switch {
	case errors.As(err, &takeover):
		return takeoverText(takeover)
	case errors.As(err, &notStarted):
		return i18n.T("background.not_started", notStarted.Log)
	case errors.Is(err, initsys.ErrUnsupportedInit):
		return i18n.T("tui.boot_unsupported")
	case errors.As(err, &nobin):
		return i18n.T("service.no_binary", nobin.Path, nobin.Path)
	case errors.As(err, &unsafe):
		return unsafe.Text()
	case errors.Is(err, ops.ErrStopTimeout):
		return i18n.T("stop.timeout")
	case errors.As(err, &already):
		return i18n.T("tui.already", i18n.Enum("mode", already.Mode))
	case errors.As(err, &conflict):
		msgs := []string{i18n.T("run.conflict")}
		for _, c := range conflict.Checks {
			msgs = append(msgs, c.Msg)
		}
		return strings.Join(msgs, " ")
	case errors.Is(err, render.ErrNoStrategy):
		return i18n.T("tui.no_method")
	case errors.Is(err, ops.ErrEngineMissing):
		return i18n.T("tui.engine_missing")
	case errors.Is(err, render.ErrNoApps), errors.Is(err, ops.ErrAppsModeEmpty):
		return i18n.T("settings_mode.empty")
	case errors.As(err, &unknownApp):
		return i18n.T("tui.unknown_app", unknownApp.ID)
	case errors.As(err, &badStrategy):
		return i18n.T("tui.bad_strategy")
	}
	// fitNote drops a note's first lines to fit, so a raw error stays on one
	// line: its cause comes first, before the rule and caret lines nft adds.
	return strings.ReplaceAll(strings.TrimSpace(err.Error()), "\n", " ")
}

// ring keeps the last max lines written to it. Each line has the tag of
// the writer that wrote it: 0 for Write, a log stream's number for the
// writer tagged returns, so that a stream's lines can be dropped once it
// ends and the next one writes them again.
type ring struct {
	mu      sync.Mutex
	max     int
	lines   []ringLine
	partial map[int]string
}

type ringLine struct {
	tag  int
	text string
}

func (r *ring) Write(p []byte) (int, error) { return r.write(0, p) }

func (r *ring) write(tag int, p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.partial == nil {
		r.partial = map[int]string{}
	}
	parts := strings.Split(r.partial[tag]+string(p), "\n")
	r.partial[tag] = parts[len(parts)-1]
	for _, l := range parts[:len(parts)-1] {
		r.lines = append(r.lines, ringLine{tag, l})
	}
	if over := len(r.lines) - r.max; over > 0 {
		r.lines = r.lines[over:]
	}
	return len(p), nil
}

type taggedWriter struct {
	r   *ring
	tag int
}

func (w taggedWriter) Write(p []byte) (int, error) { return w.r.write(w.tag, p) }

// tagged is a writer whose lines carry tag.
func (r *ring) tagged(tag int) io.Writer { return taggedWriter{r, tag} }

// drop removes the lines tagged tag.
func (r *ring) drop(tag int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lines = slices.DeleteFunc(r.lines, func(l ringLine) bool { return l.tag == tag })
	delete(r.partial, tag)
}

// Last returns up to n of the newest complete lines.
func (r *ring) Last(n int) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	n = min(n, len(r.lines))
	out := make([]string, n)
	for i, l := range r.lines[len(r.lines)-n:] {
		out[i] = l.text
	}
	return out
}

func (r *ring) String() string { return strings.Join(r.Last(r.max), "\n") }
