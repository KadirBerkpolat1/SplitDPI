package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/KadirBerkpolat1/SplitDPI/internal/config"
	"github.com/KadirBerkpolat1/SplitDPI/internal/dns"
	"github.com/KadirBerkpolat1/SplitDPI/internal/doctor"
	"github.com/KadirBerkpolat1/SplitDPI/internal/engine"
	"github.com/KadirBerkpolat1/SplitDPI/internal/i18n"
	"github.com/KadirBerkpolat1/SplitDPI/internal/initsys"
	"github.com/KadirBerkpolat1/SplitDPI/internal/ops"
	"github.com/KadirBerkpolat1/SplitDPI/internal/presets"
	"github.com/KadirBerkpolat1/SplitDPI/internal/probe"
	"github.com/KadirBerkpolat1/SplitDPI/internal/scan"
	"github.com/KadirBerkpolat1/SplitDPI/internal/supervisor"
	"github.com/KadirBerkpolat1/SplitDPI/internal/sysinfo"
)

func TestMain(m *testing.M) {
	i18n.Init("en")
	os.Exit(m.Run())
}

// fake is the backend the TUI drives; each method records or returns canned data.
type fake struct {
	mu         sync.Mutex
	st         supervisor.Status
	cfg        config.Config
	runCtx     context.Context
	runStart   chan struct{}
	bgErr      error
	logs       string
	logsFollow bool // Logs blocks until cancelled, like a real follow
	logsErr    error
	calls      []string
	checks     []doctor.Check
	set        presets.Set
	detected   scan.ASNInfo
	scanFn     func(ctx context.Context, opt scan.Options, progress func(string)) (scan.Report, error)
	excluded   []string
	sites      []string
	strategies []ops.StrategyChoice
	ipv6       bool

	probeRes      map[string][]probe.Result
	importAdded   []string
	importSkipped []ops.ImportSkip
	importErr     error
	foreign       []ops.Foreign
	takeoverRep   ops.TakeoverReport

	plan         ops.UninstallPlan
	uninstallRep ops.UninstallReport
	uninstallErr error

	engineErr error
	engineDir string // the installed engine, "" if none

	systemDNS string // the resolver that already encrypts DNS, if any

	presetsErr error
	takenOver  []string
	revertRep  ops.RevertReport
}

func newFake() *fake {
	cfg := config.Default()
	cfg.Apps = []string{"discord", "youtube"}
	cfg.DisclaimerAccepted = true
	return &fake{st: supervisor.Status{State: "stopped"}, cfg: cfg, runStart: make(chan struct{}, 1)}
}

func (f *fake) record(s string) {
	f.mu.Lock()
	f.calls = append(f.calls, s)
	f.mu.Unlock()
}

func (f *fake) called(s string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Contains(f.calls, s)
}

func (f *fake) Status() (supervisor.Status, config.Config, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.st, f.cfg, nil
}
func (f *fake) Run(ctx context.Context, service bool) error {
	f.mu.Lock()
	f.runCtx = ctx
	f.st.State = "running"
	f.mu.Unlock()
	f.runStart <- struct{}{}
	<-ctx.Done()
	f.record("run ended")
	return nil
}
func (f *fake) Background(ctx context.Context) (int, error) {
	f.record("background")
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.bgErr != nil {
		return 0, f.bgErr
	}
	f.st = supervisor.Status{State: "running", Mode: "background", PID: 4343}
	return 4343, nil
}
func (f *fake) Stop() error { f.record("stop"); return nil }
func (f *fake) Logs(ctx context.Context, follow bool, w io.Writer) error {
	f.record("logs")
	io.WriteString(w, f.logs)
	if f.logsFollow {
		<-ctx.Done()
	}
	return f.logsErr
}
func (f *fake) Probe(ctx context.Context, apps []string) (map[string][]probe.Result, error) {
	f.record("probe")
	if f.probeRes != nil {
		return f.probeRes, nil
	}
	return map[string][]probe.Result{"discord": {{URL: "https://discord.com/api/v9/gateway", OK: true}}}, nil
}
func (f *fake) Doctor(ctx context.Context) []doctor.Check { f.record("doctor"); return f.checks }
func (f *fake) IPInfoHost() (string, error)               { return "ipinfo.io", nil }
func (f *fake) Detect(ctx context.Context, asn int, country string) (scan.ASNInfo, []presets.Candidate, error) {
	f.record("detect")
	return f.detected, nil, nil
}
func (f *fake) PresetsList() presets.Set { return f.set }
func (f *fake) AppsEnable(id string) error {
	f.record("apps enable " + id)
	return nil
}
func (f *fake) AppsDisable(id string) error {
	f.record("apps disable " + id)
	return nil
}
func (f *fake) ExcludeList() ([]string, error) { return f.excluded, nil }
func (f *fake) ExcludeAdd(d string) (string, error) {
	f.record("exclude add " + d)
	n := strings.ToLower(strings.TrimSuffix(strings.TrimPrefix(d, "https://"), "/"))
	f.excluded = append(f.excluded, n)
	return n, nil
}
func (f *fake) SitesList() ([]string, error) { return f.sites, nil }
func (f *fake) SitesAdd(d string) (string, error) {
	f.record("sites add " + d)
	n := strings.ToLower(strings.TrimSuffix(strings.TrimPrefix(d, "https://"), "/"))
	f.sites = append(f.sites, n)
	return n, nil
}
func (f *fake) SitesRemove(d string) error {
	f.record("sites remove " + d)
	f.sites = slices.DeleteFunc(f.sites, func(s string) bool { return s == d })
	return nil
}
func (f *fake) StrategyList() ([]ops.StrategyChoice, *config.Strategy, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.strategies, f.cfg.Strategy, nil
}
func (f *fake) StrategySet(id string) (ops.StrategyChoice, bool, error) {
	f.record("strategy set " + id)
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, ch := range f.strategies {
		if ch.Strategy.ID == id {
			f.cfg.Strategy = &config.Strategy{ID: id, Source: ch.Source}
			return ch, f.st.State == "running", nil
		}
	}
	return ops.StrategyChoice{}, false, ops.UnknownStrategyError{ID: id}
}
func (f *fake) Scan(ctx context.Context, opt scan.Options, progress func(string)) (scan.Report, error) {
	return f.scanFn(ctx, opt, progress)
}
func (f *fake) HasIPv6(ctx context.Context) bool { return f.ipv6 }
func (f *fake) Enable(ctx context.Context) error { f.record("enable"); return nil }
func (f *fake) AcceptDisclaimer() error {
	f.mu.Lock()
	f.cfg.DisclaimerAccepted = true
	f.mu.Unlock()
	f.record("accept disclaimer")
	return nil
}
func (f *fake) SetLang(lang string) error { f.record("lang " + lang); return nil }
func (f *fake) SetRunMode(mode config.RunMode) error {
	f.record("run mode " + string(mode))
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cfg.RunMode = mode
	return nil
}

func (f *fake) SitesImport(path string) ([]string, []ops.ImportSkip, error) {
	f.record("sites import " + path)
	if f.importErr != nil {
		return nil, nil, f.importErr
	}
	f.sites = append(f.sites, f.importAdded...)
	return f.importAdded, f.importSkipped, nil
}
func (f *fake) DetectForeign(ctx context.Context) ([]ops.Foreign, error) {
	f.record("detect foreign")
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.foreign), nil
}

// Takeover leaves the install stopped and off at boot, as the real one does.
func (f *fake) Takeover(ctx context.Context, fr ops.Foreign) (ops.TakeoverReport, error) {
	f.record("takeover " + fr.Kind)
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.foreign {
		if f.foreign[i].Kind == fr.Kind {
			f.foreign[i].Running, f.foreign[i].Service.Enabled = false, false
		}
	}
	if fr.Service.Name != "" {
		f.takenOver = append(f.takenOver, fr.Service.Name+" ("+string(fr.Service.Init)+")")
	}
	return f.takeoverRep, nil
}

func (f *fake) DNSProviders() []ops.DNSProvider {
	out := []ops.DNSProvider{
		{ID: dns.Off, Name: i18n.T("dns2.off_name"), Note: i18n.T("dns2.off_note")},
		{ID: dns.Auto, Name: i18n.T("dns2.auto_name"), Note: i18n.T("dns2.auto_note")},
	}
	for _, p := range dns.Providers {
		out = append(out, ops.DNSProvider{ID: p.ID, Name: p.Name, Note: i18n.T("dns2.note_" + p.ID)})
	}
	return out
}
func (f *fake) SetDNS(provider string, proto dns.Protocol) (bool, error) {
	f.record("dns " + provider + " " + string(proto))
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cfg.DNSProvider, f.cfg.DNSProtocol = provider, proto
	return f.st.State == "running", nil
}
func (f *fake) SystemEncryptedDNS(context.Context) (string, bool) {
	return f.systemDNS, f.systemDNS != ""
}
func (f *fake) UninstallPlan() ops.UninstallPlan { return f.plan }
func (f *fake) Uninstall(ctx context.Context) (ops.UninstallReport, error) {
	f.record("uninstall")
	return f.uninstallRep, f.uninstallErr
}

// EngineInstall fails with engineErr, else installs: the engine check passes.
func (f *fake) EngineInstall(ctx context.Context, from string) (engine.Engine, error) {
	f.record("engine install " + from)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.engineErr != nil {
		return engine.Engine{}, f.engineErr
	}
	f.checks = slices.DeleteFunc(f.checks, func(c doctor.Check) bool { return c.ID == "engine" })
	f.engineDir = "/opt/splitdpi/zapret2-" + engine.Version
	return engine.Engine{Dir: f.engineDir}, nil
}

func (f *fake) EngineStatus() (engine.Engine, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return engine.Engine{Dir: f.engineDir}, f.engineDir != ""
}

// The config setters check their values the way ops does.
func (f *fake) SetMode(mode config.Mode) error {
	f.record("mode " + string(mode))
	f.mu.Lock()
	defer f.mu.Unlock()
	if mode == config.ModeApps && len(f.cfg.Apps) == 0 && len(f.sites) == 0 {
		return ops.ErrAppsModeEmpty
	}
	f.cfg.Mode = mode
	return nil
}
func (f *fake) SetSkipIfaces(list []string) error {
	f.record("skip_ifaces " + strings.Join(list, ","))
	for _, i := range list {
		if strings.ContainsAny(i, " /") || len(i) > 15 {
			return errors.New("invalid interface name " + i)
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cfg.SkipIfaces = slices.Clone(list)
	return nil
}
func (f *fake) SetIPInfoURL(u string) error {
	f.record("ipinfo_url " + u)
	if u == "" {
		u = config.Default().IPInfoURL
	}
	if !strings.HasPrefix(u, "https://") || len(u) == len("https://") {
		return errors.New("not an https URL: " + u)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cfg.IPInfoURL = u
	return nil
}
func (f *fake) SetQNum(n int) error {
	f.record(fmt.Sprintf("qnum %d", n))
	if n < 0 || n > 65535 {
		return fmt.Errorf("queue number %d is not between 0 and 65535", n)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cfg.QNum = n
	return nil
}
func (f *fake) PresetsUpdate(context.Context) (string, int, error) {
	f.record("presets update")
	if f.presetsErr != nil {
		return "", 0, f.presetsErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.set.Version = "2026.10.30"
	return f.set.Version, 12, nil
}
func (f *fake) TakenOver() ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.takenOver), nil
}

// Revert turns the taken-over services back on, as the real one does.
func (f *fake) Revert(ctx context.Context, rulesOnly, purge bool) ops.RevertReport {
	f.record(fmt.Sprintf("revert %v %v", rulesOnly, purge))
	f.mu.Lock()
	defer f.mu.Unlock()
	f.takenOver = nil
	return f.revertRep
}

func newModel(t *testing.T, f *fake) Model {
	t.Helper()
	m := New(context.Background(), f, "v0.1.0")
	m.every = func(time.Duration, func(time.Time) tea.Msg) tea.Cmd { return nil }
	m.width, m.height = 80, 24
	m.g = unicodeGlyphs
	return m
}

func key(s string) tea.KeyMsg {
	switch s {
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "space":
		return tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func update(t *testing.T, m Model, msg tea.Msg) (Model, tea.Cmd) {
	t.Helper()
	next, cmd := m.Update(msg)
	return next.(Model), cmd
}

// press sends a key and runs the resulting command once, feeding its message back.
func press(t *testing.T, m Model, k string) Model {
	t.Helper()
	m, cmd := update(t, m, key(k))
	return settle(t, m, cmd)
}

func typeText(t *testing.T, m Model, s string) Model {
	t.Helper()
	for _, r := range s {
		m, _ = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	return m
}

// settle runs cmd and feeds results back until nothing is left. Batches are
// expanded; ticks never run because tests disable m.every.
func settle(t *testing.T, m Model, cmd tea.Cmd) Model {
	t.Helper()
	queue := []tea.Cmd{cmd}
	for i := 0; len(queue) > 0 && i < 50; i++ {
		c := queue[0]
		queue = queue[1:]
		if c == nil {
			continue
		}
		msg := c()
		if b, ok := msg.(tea.BatchMsg); ok {
			queue = append(queue, b...)
			continue
		}
		if _, ok := msg.(tea.QuitMsg); ok {
			continue
		}
		var next tea.Cmd
		m, next = update(t, m, msg)
		queue = append(queue, next)
	}
	return m
}

// goTo moves the menu to page pg with the arrow keys and goes into the page.
func goTo(t *testing.T, m Model, pg int) Model {
	t.Helper()
	if m.inPage {
		m = press(t, m, "esc")
	}
	for m.page < pg {
		m = press(t, m, "down")
	}
	for m.page > pg {
		m = press(t, m, "up")
	}
	return press(t, m, "enter")
}

var missingKey = regexp.MustCompile(`![a-z_]+(\.[a-z_]+)+!`)

func has(t *testing.T, view, want string) {
	t.Helper()
	if !strings.Contains(view, want) {
		t.Errorf("view does not contain %q:\n%s", want, view)
	}
	if k := missingKey.FindString(view); k != "" {
		t.Errorf("missing i18n key %s in view:\n%s", k, view)
	}
}

func TestStatusScreen(t *testing.T) {
	f := newFake()
	v := newModel(t, f).View()
	has(t, v, "stopped")
	has(t, v, "none, run setup")
	has(t, v, "discord")
	has(t, v, "youtube")
	has(t, v, "r Run")
	has(t, v, "sudo splitdpi scan") // the setup comes first without a strategy
}

func TestHeader(t *testing.T) {
	f := newFake()
	v := newModel(t, f).View()
	head := strings.Split(v, "\n")[0]
	if !strings.HasPrefix(head, "◆ SplitDPI v0.1.0") || !strings.HasSuffix(head, " ○ STOPPED ") || lipgloss.Width(head) != 80 {
		t.Errorf("header %q", head)
	}
	f.st.State = "failed"
	has(t, newModel(t, f).View(), " ● FAILED ")
	f.st.State = "running"
	has(t, newModel(t, f).View(), " ● RUNNING ")
}

func TestStatusSetupOnlyWithoutStrategy(t *testing.T) {
	f := newFake()
	f.cfg.Strategy = &config.Strategy{ID: "tt-multidisorder"}
	m := press(t, newModel(t, f), "w")
	if m.page != pgStatus || f.called("doctor") {
		t.Errorf("w opened the setup although a strategy is set: page %d", m.page)
	}
}

func TestLongTextWrapsInsteadOfCut(t *testing.T) {
	m := press(t, newModel(t, newFake()), "w")
	v := pages[pgScan].View(m, 60, 18)
	if !strings.Contains(strings.Join(strings.Fields(v), " "), "This sends one request to that site.") {
		t.Errorf("lookup question cut off:\n%s", v)
	}
	for _, l := range strings.Split(v, "\n") {
		if lipgloss.Width(l) > 60 {
			t.Errorf("line wider than the page: %q", l)
		}
	}
}

func TestFocusMovesFooterCommand(t *testing.T) {
	f := newFake()
	f.cfg.Strategy = &config.Strategy{ID: "tt-multidisorder"}
	m := press(t, newModel(t, f), "enter")
	has(t, m.View(), "$ sudo splitdpi run")
	m = press(t, m, "tab")
	has(t, m.View(), "$ sudo splitdpi run --background")
	m = press(t, m, "tab")
	has(t, m.View(), "$ sudo splitdpi probe")
}

func TestMenuNavigation(t *testing.T) {
	f := newFake()
	f.set = trSet()
	f.sites = []string{"pastebin.com"}
	m := newModel(t, f)
	m = press(t, m, "up")
	if m.page != pgStatus {
		t.Fatalf("up from the first entry: page %d", m.page)
	}
	m = press(t, m, "down")
	if m.page != pgSites || m.inPage {
		t.Fatalf("down: page %d inPage %v", m.page, m.inPage)
	}
	has(t, m.View(), "Discord") // Open loaded the page at once
	has(t, m.View(), "pastebin.com")
	m = press(t, press(t, m, "j"), "k")
	if m.page != pgSites {
		t.Fatalf("j then k: page %d", m.page)
	}
	m = press(t, m, "enter")
	if !m.inPage {
		t.Fatal("enter did not move focus into the page")
	}
	m = press(t, m, "down")
	if m.page != pgSites || m.apps.cursor != 1 {
		t.Errorf("down in the page: page %d cursor %d; want the cursor to move", m.page, m.apps.cursor)
	}
	m = press(t, m, "esc")
	if m.inPage || m.page != pgSites {
		t.Errorf("esc: inPage %v page %d", m.inPage, m.page)
	}
	m = press(t, m, "tab")
	if !m.inPage {
		t.Error("tab did not move focus into the page")
	}
	for range pages {
		m = press(t, press(t, m, "esc"), "down")
	}
	if m.page != len(pages)-1 {
		t.Errorf("down past the last entry: page %d", m.page)
	}
}

func TestMouseClickSelectsMenuEntry(t *testing.T) {
	f := newFake()
	f.strategies = testStrategies()
	m := press(t, newModel(t, f), "enter")
	lines := strings.Split(m.View(), "\n")
	for i, p := range pages {
		if !strings.Contains(lines[menuTop+i], p.Title()) {
			t.Errorf("menu entry %q is not on line %d: %q", p.Title(), menuTop+i, lines[menuTop+i])
		}
	}
	click := func(m Model, x, y int) Model {
		m, cmd := update(t, m, tea.MouseMsg{X: x, Y: y, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
		return settle(t, m, cmd)
	}
	m = click(m, 5, menuTop+pgStrategy)
	if m.page != pgStrategy || m.inPage {
		t.Fatalf("click: page %d inPage %v", m.page, m.inPage)
	}
	has(t, m.View(), "tt-multidisorder")
	m = click(m, 40, menuTop+pgLogs)
	m = click(m, 5, menuTop+len(pages)+3)
	if m.page != pgStrategy {
		t.Errorf("a click outside the menu changed the page to %d", m.page)
	}
}

func TestHelpOverlay(t *testing.T) {
	f := newFake()
	f.set = trSet()
	m := press(t, press(t, newModel(t, f), "down"), "?")
	v := m.View()
	has(t, v, "Keys on Sites")
	has(t, v, "Add a site")
	has(t, v, "A site broke")
	has(t, v, "Everywhere")
	has(t, v, "Back to the menu")
	m = press(t, m, "down")
	if m.page != pgSites || !m.help {
		t.Errorf("a key reached the menu under the help: page %d help %v", m.page, m.help)
	}
	if m = press(t, m, "esc"); m.help {
		t.Error("esc did not close the help")
	}
	m = press(t, press(t, m, "?"), "?")
	if m.help || strings.Contains(m.View(), "Everywhere") {
		t.Error("? did not close the help")
	}
}

func TestGlyphsFor(t *testing.T) {
	for _, c := range []struct {
		env   map[string]string
		ascii bool
	}{
		{map[string]string{"LANG": "en_US.UTF-8"}, false},
		{map[string]string{"LANG": "tr_TR.utf8"}, false},
		{map[string]string{"LC_CTYPE": "C.UTF-8", "LANG": "C"}, false},
		{map[string]string{"LC_ALL": "C", "LANG": "en_US.UTF-8"}, true},
		{map[string]string{"LANG": "C"}, true},
		{map[string]string{"LANG": "POSIX"}, true},
		{map[string]string{}, true},
		{map[string]string{"LANG": "en_US.UTF-8", "TERM": "linux"}, true},
	} {
		g := glyphsFor(func(k string) string { return c.env[k] })
		if got := g.cursor == asciiGlyphs.cursor; got != c.ascii {
			t.Errorf("%v: ascii %v, want %v", c.env, got, c.ascii)
		}
	}
}

func TestASCIIFallback(t *testing.T) {
	u := unicodeGlyphs
	symbols := u.border.Top + u.border.Left + u.border.TopLeft + u.border.TopRight + u.border.BottomLeft + u.border.BottomRight +
		u.logo + u.dot + u.dotOff + u.cursor + u.menuMark + u.on + u.off + u.fail + u.bullet + u.sep + u.menuKeys +
		u.bar[0] + u.bar[1] + u.bar[2]
	for _, ascii := range []bool{true, false} {
		for name, v := range pageViews(t, func(m Model) Model {
			if ascii {
				m.g = asciiGlyphs
			}
			return m
		}) {
			// Names such as Türk Telekom or Türkçe are text, not drawing.
			if i := strings.IndexAny(v, symbols); ascii && i >= 0 {
				t.Errorf("ascii %s has the symbol %q:\n%s", name, []rune(v[i:])[0], v)
			}
			if !ascii && !strings.Contains(v, "╭") {
				t.Errorf("unicode %s has no rounded border:\n%s", name, v)
			}
		}
	}
	m := newModel(t, newFake())
	m.g = asciiGlyphs
	v := m.View()
	for _, want := range []string{"+--------------+", "| > Status", "* SplitDPI", "* STOPPED"} {
		has(t, v, want)
	}
}

func TestConfirmDialog(t *testing.T) {
	yes := 0
	m := newModel(t, newFake())
	ask := func(m Model) Model {
		m.dialog = &confirm{
			title:   "Do the thing?",
			changes: []string{"First change", strings.Repeat("a long change that wraps ", 5)},
			undo:    "sudo splitdpi undo-it",
			yes:     func(*Model) tea.Cmd { yes++; return nil },
		}
		return m
	}
	m = ask(m)
	v := m.View()
	for _, want := range []string{"Do the thing?", "This will:", "• First change", "To undo it later, run:", "sudo splitdpi undo-it", "y Yes, go ahead   n Cancel"} {
		has(t, v, want)
	}
	for _, l := range strings.Split(v, "\n") {
		if lipgloss.Width(l) > 80 {
			t.Errorf("dialog line %d wide: %q", lipgloss.Width(l), l)
		}
	}
	m = press(t, m, "down")
	if m.dialog == nil || m.page != pgStatus {
		t.Fatal("a key went past the dialog")
	}
	m = press(t, m, "n")
	if m.dialog != nil || yes != 0 {
		t.Fatalf("n: dialog %v, confirmed %d times", m.dialog, yes)
	}
	m = press(t, ask(m), "esc")
	if m.dialog != nil || yes != 0 {
		t.Fatalf("esc: dialog %v, confirmed %d times", m.dialog, yes)
	}
	m = press(t, ask(m), "y")
	if m.dialog != nil || yes != 1 {
		t.Fatalf("y: dialog %v, confirmed %d times", m.dialog, yes)
	}

	defer i18n.Init("en")
	i18n.Init("tr")
	m = ask(m)
	v = m.View()
	has(t, v, "e Evet, devam et   h Vazgeç")
	for _, l := range strings.Split(v, "\n") {
		if lipgloss.Width(l) > 80 {
			t.Errorf("tr dialog line %d wide: %q", lipgloss.Width(l), l)
		}
	}
	m = press(t, m, "h")
	if m.dialog != nil || yes != 1 {
		t.Fatalf("h: dialog %v, confirmed %d times", m.dialog, yes)
	}
	m = press(t, ask(m), "e")
	if m.dialog != nil || yes != 2 {
		t.Fatalf("e: dialog %v, confirmed %d times", m.dialog, yes)
	}
}

func TestTooSmallTerminal(t *testing.T) {
	for _, size := range [][2]int{{79, 24}, {80, 23}, {40, 10}} {
		m := newModel(t, newFake())
		m, _ = update(t, m, tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		v := m.View()
		if strings.Contains(v, "\n") || lipgloss.Width(v) > size[0] {
			t.Errorf("%v: not one line that fits: %q", size, v)
		}
		if size[0] >= 40 {
			has(t, v, "Enlarge the terminal")
		}
		if _, cmd := update(t, m, key("q")); cmd == nil {
			t.Errorf("%v: q does not quit", size)
		}
	}
}

func TestSettingsLanguage(t *testing.T) {
	defer i18n.Init("en")
	f := newFake()
	m := goTo(t, newModel(t, f), pgSettings)
	has(t, m.View(), "Language")
	has(t, m.View(), "English")
	has(t, m.View(), "Space Switch to Türkçe")
	m = press(t, m, "space")
	if i18n.Lang() != "tr" || !f.called("lang tr") {
		t.Fatalf("lang %s, calls %q", i18n.Lang(), f.calls)
	}
	v := m.View()
	has(t, v, "Ayarlar")
	has(t, v, "Türkçe")
	has(t, v, "Dil kaydedildi.")
	has(t, v, "Boşluk Dili değiştir: English")
	press(t, m, "space")
	if i18n.Lang() != "en" || !f.called("lang en") {
		t.Errorf("lang %s, calls %q", i18n.Lang(), f.calls)
	}
}

func TestLogsScreen(t *testing.T) {
	f := newFake()
	f.logs = "nfqws2 started (pid 42)\n"
	f.logsFollow = true
	m := newModel(t, f)
	m.page = pgLogs - 1
	m, cmd := update(t, m, key("down"))
	if m.page != pgLogs || m.logCancel == nil || cmd == nil {
		t.Fatalf("page %d, stream started %v", m.page, m.logCancel != nil)
	}
	has(t, m.View(), "sudo splitdpi logs -f")
	go cmd() // the stream and the first tick; the stream blocks until cancelled
	deadline := time.Now().Add(2 * time.Second)
	for !strings.Contains(m.logs.String(), "nfqws2 started") && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	m, _ = update(t, m, logTickMsg{})
	has(t, m.View(), "nfqws2 started (pid 42)")
	// Leaving and coming back does not start a second stream (ticks are nil
	// in tests, so any command would be one).
	m, _ = update(t, m, key("down"))
	m, _ = update(t, m, logTickMsg{})
	m, cmd = update(t, m, key("up"))
	if m.page != pgLogs || cmd != nil {
		t.Errorf("back on the logs: page %d, a second stream %v", m.page, cmd != nil)
	}
	m.stopAll()
}

// A log stream that cannot start says why instead of looking empty.
func TestLogsStreamError(t *testing.T) {
	f := newFake()
	f.logsErr = errors.New(`exec: "journalctl": executable file not found in $PATH`)
	m := newModel(t, f)
	m.page = pgLogs - 1
	m = press(t, m, "down")
	v := m.View()
	has(t, v, `exec: "journalctl": executable file not found`)
	if strings.Contains(v, i18n.T("tui.logs_empty")) {
		t.Errorf("failed stream shown as an empty log:\n%s", v)
	}
	m.stopAll()
}

func TestQuit(t *testing.T) {
	_, cmd := update(t, newModel(t, newFake()), key("q"))
	if cmd == nil {
		t.Fatal("no command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Error("q did not quit")
	}
}

func TestTickRefreshesStatus(t *testing.T) {
	f := newFake()
	m := newModel(t, f)
	ticks := 0
	m.every = func(time.Duration, func(time.Time) tea.Msg) tea.Cmd { ticks++; return nil }
	f.mu.Lock()
	f.st = supervisor.Status{State: "running", Mode: "service", PID: 77}
	f.mu.Unlock()
	m, cmd := update(t, m, tickMsg{})
	m = settle(t, m, cmd)
	has(t, m.View(), "running")
	if ticks == 0 {
		t.Error("tick did not schedule the next one")
	}
}

func TestProbeShowsResults(t *testing.T) {
	f := newFake()
	m := press(t, newModel(t, f), "p")
	if !f.called("probe") {
		t.Fatal("probe not called")
	}
	has(t, m.View(), "1/1 sites open")
	has(t, m.View(), "all on the Sites page")
}

// Run now keeps the bypass inside this process; quitting must ask, then revert.
func TestRunNowAsksBeforeQuit(t *testing.T) {
	f := newFake()
	m := newModel(t, f)
	m, cmd := update(t, m, key("r"))
	done := make(chan Model)
	go func() { done <- settle(t, m, cmd) }()
	<-f.runStart
	if !m.running {
		t.Fatal("model does not know it runs the bypass")
	}
	has(t, m.View(), "● RUNNING")
	m, cmd = update(t, m, key("q"))
	if cmd != nil {
		if _, ok := cmd().(tea.QuitMsg); ok {
			t.Fatal("quit without asking while the bypass runs")
		}
	}
	has(t, m.View(), "The bypass runs in this window")
	m, cmd = update(t, m, key("y"))
	if f.runCtx.Err() == nil {
		t.Error("run not cancelled")
	}
	<-done
	m = settle(t, m, cmd)
	_ = m
}

// A run started and stopped in the TUI is over: quitting has nothing to stop.
func TestQuitAfterStoppedRun(t *testing.T) {
	f := newFake()
	m := newModel(t, f)
	m, cmd := update(t, m, key("r"))
	done := make(chan Model)
	go func() { done <- settle(t, m, cmd) }()
	<-f.runStart
	m, cmd = update(t, m, key("s"))
	if f.runCtx.Err() == nil {
		t.Fatal("s did not cancel the run")
	}
	m = settle(t, m, cmd)
	m, _ = update(t, m, runDoneMsg{})
	<-done
	if m.running {
		t.Fatal("still running after the run ended")
	}
	m, cmd = update(t, m, key("q"))
	if cmd == nil {
		t.Fatal("q does not quit after the run stopped")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("q does not quit after the run stopped")
	}
	m.stopRun()
}

func trSet() presets.Set {
	return presets.Set{
		ISPs: []presets.ISP{{Name: "Türk Telekom", Country: "TR", ASN: []int{9121}, File: "isp/tr/as9121-turk-telekom.toml"}},
		Apps: map[string]presets.App{
			"discord": {ID: "discord", Name: "Discord"},
			"youtube": {ID: "youtube", Name: "YouTube"},
		},
		Countries: map[string]presets.Country{"TR": {Code: "TR", DefaultApps: []string{"discord"}}},
	}
}

func TestDisclaimerFirstStart(t *testing.T) {
	f := newFake()
	f.cfg.DisclaimerAccepted = false
	m := newModel(t, f)
	has(t, m.View(), i18n.T("disclaimer.title"))
	if strings.Contains(m.View(), "none, run setup") || strings.Contains(m.View(), "Settings") {
		t.Error("status or menu shown before the disclaimer was accepted")
	}
	m = press(t, m, "down")
	if m.page != pgStatus {
		t.Error("the menu moved under the disclaimer")
	}
	m = press(t, m, "enter")
	if !f.called("accept disclaimer") {
		t.Fatal("acceptance not saved")
	}
	has(t, m.View(), "stopped")
}

func TestWizardStopsOnDoctorBlocker(t *testing.T) {
	f := newFake()
	f.checks = []doctor.Check{{ID: "nft", Severity: doctor.Blocker, Msg: "nft is not installed", Fix: "sudo apt install nftables"}}
	m := press(t, newModel(t, f), "w")
	if m.page != pgScan || !m.inPage {
		t.Fatalf("w: page %d inPage %v; want the scan page focused", m.page, m.inPage)
	}
	has(t, m.View(), "sudo apt install nftables")
	has(t, m.View(), "✗ nft is not installed")
	m = press(t, m, "enter") // check again
	if m.page != pgScan || m.wiz.step != wDoctor {
		t.Errorf("left the doctor step with a blocker: page %d step %v", m.page, m.wiz.step)
	}
	has(t, m.View(), "sudo splitdpi doctor")
}

func TestScanPageStartsSetup(t *testing.T) {
	f := newFake()
	f.cfg.Strategy = &config.Strategy{ID: "tt-multidisorder"}
	m := goTo(t, newModel(t, f), pgScan)
	has(t, m.View(), "The setup checks this machine")
	has(t, m.View(), "$ sudo splitdpi scan")
	if f.called("doctor") {
		t.Fatal("selecting the page started the setup")
	}
	m = press(t, m, "enter")
	if !f.called("doctor") || m.wiz.step != wLookup {
		t.Errorf("enter: step %v, calls %q", m.wiz.step, f.calls)
	}
	// The wizard keeps its place while another page is shown.
	m = press(t, press(t, press(t, m, "esc"), "up"), "down")
	if m.wiz.step != wLookup {
		t.Errorf("step %v after leaving the page", m.wiz.step)
	}
}

func TestWizardDeclineLookupShowsPicker(t *testing.T) {
	f := newFake()
	f.set = trSet()
	m := press(t, newModel(t, f), "w")
	has(t, m.View(), "ipinfo.io")
	m = press(t, m, "n")
	has(t, m.View(), "Türk Telekom")
	has(t, m.View(), "sudo splitdpi detect --asn 9121 --country TR")
	if f.called("detect") {
		t.Error("IP lookup ran although it was declined")
	}
}

// toScan walks the wizard to the scan step and returns the blocking scan command.
func toScan(t *testing.T, f *fake) (Model, tea.Cmd) {
	t.Helper()
	f.set = trSet()
	f.detected = scan.ASNInfo{ASN: 9121, Country: "TR", Org: "Turk Telekom"}
	f.cfg.Apps = nil
	m := press(t, newModel(t, f), "w")
	m = press(t, m, "y")
	if m.wiz.step != wApps || !m.wiz.chosen["discord"] || m.wiz.chosen["youtube"] {
		t.Fatalf("apps step %v, chosen %v; want TR defaults preselected", m.wiz.step, m.wiz.chosen)
	}
	has(t, m.View(), "sudo splitdpi apps")
	m, cmd := update(t, m, key("enter"))
	m, cmd = update(t, m, cmd()) // apps applied → scan starts
	if !f.called("apps enable discord") {
		t.Errorf("chosen apps not saved: %q", f.calls)
	}
	has(t, m.View(), "sudo splitdpi scan --asn 9121 --country TR --apps discord")
	return m, cmd
}

func TestWizardScanStreamsAndCancels(t *testing.T) {
	f := newFake()
	started := make(chan context.Context, 1)
	f.scanFn = func(ctx context.Context, opt scan.Options, progress func(string)) (scan.Report, error) {
		progress("trying tt-multidisorder")
		started <- ctx
		<-ctx.Done()
		return scan.Report{}, ctx.Err()
	}
	m, cmd := toScan(t, f)
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	scanCtx := <-started
	has(t, m.View(), "trying tt-multidisorder")
	m, _ = update(t, m, key("esc"))
	if scanCtx.Err() == nil {
		t.Fatal("esc did not cancel the scan")
	}
	m, _ = update(t, m, <-done)
	has(t, m.View(), "cancelled")
	if m.page != pgScan || !m.inPage {
		t.Errorf("esc during the scan left the page: page %d inPage %v", m.page, m.inPage)
	}
}

func TestWizardFinishStartAtBoot(t *testing.T) {
	f := newFake()
	f.scanFn = func(context.Context, scan.Options, func(string)) (scan.Report, error) {
		return scan.Report{Winner: &config.Strategy{ID: "tt-multidisorder", Source: "isp/tr/as9121-turk-telekom.toml"}}, nil
	}
	m, cmd := toScan(t, f)
	m = settle(t, m, cmd)
	has(t, m.View(), "tt-multidisorder")
	has(t, m.View(), "sudo splitdpi run")
	m = press(t, m, "b")
	if f.called("enable") {
		t.Fatal("enabled without asking")
	}
	has(t, m.View(), "Start SplitDPI at boot?")
	has(t, m.View(), "sudo splitdpi disable")
	m = press(t, m, "n")
	if f.called("enable") || m.wiz.step != wDone {
		t.Fatalf("cancel: step %v, calls %q", m.wiz.step, f.calls)
	}
	m = press(t, press(t, m, "b"), "y")
	if !f.called("enable") {
		t.Fatal("start at boot did not enable the service")
	}
	if m.page != pgStatus || m.wiz.step != wIntro {
		t.Errorf("after enabling: page %d step %v; want status and a fresh setup", m.page, m.wiz.step)
	}
	has(t, m.View(), i18n.T("service.enabled"))
}

func TestWizardScanSite(t *testing.T) {
	f := newFake()
	f.set = trSet()
	f.detected = scan.ASNInfo{ASN: 9121, Country: "TR", Org: "Turk Telekom"}
	f.ipv6 = true
	var got scan.Options
	f.scanFn = func(_ context.Context, opt scan.Options, _ func(string)) (scan.Report, error) {
		got = opt
		return scan.Report{Winner: &config.Strategy{ID: "blockcheck", Source: "blockcheck2"}}, nil
	}
	m := press(t, press(t, newModel(t, f), "w"), "y")
	has(t, m.View(), "Try ready-made strategies (fast)")
	has(t, m.View(), "Scan a site with blockcheck2")
	m = press(t, m, "s")
	has(t, m.View(), "both IPv4 and IPv6")
	m = press(t, m, "esc")
	if m.page != pgScan || m.wiz.step != wApps {
		t.Fatalf("esc on the site step: page %d step %v, want the apps step", m.page, m.wiz.step)
	}
	f.ipv6 = false
	m = press(t, m, "s")
	has(t, m.View(), "only IPv4")
	m = typeText(t, m, "https://Pastebin.com/")
	has(t, m.View(), "sudo splitdpi scan --asn 9121 --country TR --site https://Pastebin.com/")
	m = press(t, m, "enter")
	if got.Site != "pastebin.com" || got.ASN != 9121 {
		t.Fatalf("scan options %+v", got)
	}
	has(t, m.View(), "blockcheck")
	has(t, m.View(), "Added pastebin.com to your sites")
}

func TestAppsSiteBroke(t *testing.T) {
	f := newFake()
	f.set = trSet()
	m := goTo(t, newModel(t, f), pgSites)
	has(t, m.View(), "Discord")
	m = press(t, m, "e")
	m = typeText(t, m, "https://Bank.example/")
	has(t, m.View(), "sudo splitdpi exclude add https://Bank.example/")
	m = press(t, m, "enter")
	if !f.called("exclude add https://Bank.example/") {
		t.Fatalf("calls %q", f.calls)
	}
	has(t, m.View(), "✗ bank.example")
	has(t, m.View(), "excluded, never touched")
}

// "a" works from the menu too, and typing then goes to the page.
func TestAppsAddFromMenu(t *testing.T) {
	f := newFake()
	f.set = trSet()
	m := press(t, press(t, newModel(t, f), "down"), "a")
	if !m.inPage || !m.apps.typing {
		t.Fatalf("a from the menu: inPage %v typing %v", m.inPage, m.apps.typing)
	}
	m = typeText(t, m, "qj")
	if m.page != pgSites || m.apps.input.Value() != "qj" {
		t.Errorf("typed keys reached the shell: page %d input %q", m.page, m.apps.input.Value())
	}
	m = press(t, m, "esc")
	if m.apps.typing || !m.inPage {
		t.Errorf("esc while typing: typing %v inPage %v", m.apps.typing, m.inPage)
	}
}

func TestAppsToggle(t *testing.T) {
	f := newFake()
	f.set = trSet()
	f.cfg.Apps = []string{"discord"}
	m := goTo(t, newModel(t, f), pgSites)
	has(t, m.View(), "$ sudo splitdpi sites add DOMAIN")
	m = press(t, m, "tab")
	has(t, m.View(), "$ sudo splitdpi apps disable discord")
	m = press(t, m, "space")
	if !f.called("apps disable discord") {
		t.Errorf("calls %q", f.calls)
	}
}

func TestAppsAddRemoveSite(t *testing.T) {
	f := newFake()
	f.set = trSet()
	m := goTo(t, newModel(t, f), pgSites)
	has(t, m.View(), "a Add a site")
	m = press(t, m, "a")
	m = typeText(t, m, "https://Pastebin.com/")
	has(t, m.View(), "sudo splitdpi sites add https://Pastebin.com/")
	m = press(t, m, "enter")
	if !f.called("sites add https://Pastebin.com/") {
		t.Fatalf("calls %q", f.calls)
	}
	has(t, m.View(), "pastebin.com")
	has(t, m.View(), "added by you")
	// The cursor runs over the two apps, then the site.
	m = press(t, press(t, press(t, m, "down"), "down"), "tab")
	has(t, m.View(), "sudo splitdpi sites remove pastebin.com")
	m = press(t, m, "x")
	if !f.called("sites remove pastebin.com") {
		t.Fatalf("calls %q", f.calls)
	}
	if len(m.apps.sites) != 0 || m.apps.cursor != 1 {
		t.Errorf("sites %q, cursor %d; want none and the last app", m.apps.sites, m.apps.cursor)
	}
	has(t, m.View(), "Removed pastebin.com")
	has(t, m.View(), "sudo splitdpi apps disable youtube")
}

func testStrategies() []ops.StrategyChoice {
	return []ops.StrategyChoice{
		{Strategy: presets.Strategy{ID: "tt-multidisorder"}, Source: "isp/tr/as9121-turk-telekom.toml", ISP: "Türk Telekom", Country: "TR"},
		{Strategy: presets.Strategy{ID: "generic-multidisorder"}, Source: "generic.toml"},
	}
}

func TestStrategyChoose(t *testing.T) {
	f := newFake()
	f.strategies = testStrategies()
	f.cfg.Strategy = &config.Strategy{ID: "tt-multidisorder"}
	m := goTo(t, newModel(t, f), pgStrategy)
	has(t, m.View(), "Türk Telekom")
	has(t, m.View(), "✓ tt-multidisorder")
	has(t, m.View(), "sudo splitdpi strategy set tt-multidisorder")
	m = press(t, m, "down")
	has(t, m.View(), "sudo splitdpi strategy set generic-multidisorder")
	m = press(t, m, "enter")
	if !f.called("strategy set generic-multidisorder") {
		t.Fatalf("calls %q", f.calls)
	}
	has(t, m.View(), "Strategy set to generic-multidisorder")
	has(t, m.View(), "✓ generic-multidisorder")
}

// pageViews renders every page and state worth seeing at 80x24, with prep
// applied to each fresh model.
func pageViews(t *testing.T, prep func(Model) Model) map[string]string {
	t.Helper()
	f := newFake()
	f.set = trSet()
	f.sites = []string{"pastebin.com"}
	f.excluded = []string{"bank.example"}
	f.strategies = testStrategies()
	f.logs = "nfqws2 started (pid 42)\n"
	f.cfg.DNSProvider = "auto"
	f.st = supervisor.Status{State: "running", Mode: "service", PID: 4242, Engine: "v1.0.6", LastError: "nfqws2 stopped (exit status 1)"}
	f.detected = scan.ASNInfo{ASN: 9121, Country: "TR", Org: "Turk Telekom"}
	f.checks = []doctor.Check{{ID: "nft", Severity: doctor.Blocker, Msg: "nft is not installed", Fix: "sudo apt install nftables"}}
	f.scanFn = func(_ context.Context, _ scan.Options, progress func(string)) (scan.Report, error) {
		progress(strings.Repeat("trying a strategy with a very long name ", 4))
		return scan.Report{Winner: &config.Strategy{ID: "tt-multidisorder", Source: "isp/tr/as9121-turk-telekom.toml"}}, nil
	}
	views := map[string]string{}
	m := prep(newModel(t, f))
	m = press(t, m, "p")
	views["status"] = m.View()
	views["help"] = press(t, m, "?").View()
	views["sites"] = goTo(t, m, pgSites).View()
	views["site broke"] = press(t, goTo(t, m, pgSites), "e").View()
	views["add site"] = press(t, goTo(t, m, pgSites), "a").View()
	views["strategy"] = goTo(t, m, pgStrategy).View()
	views["scan intro"] = goTo(t, m, pgScan).View()
	lg := goTo(t, m, pgLogs)
	views["logs"] = lg.View()
	lg.stopAll()
	views["settings"] = goTo(t, m, pgSettings).View()

	w := press(t, goTo(t, m, pgScan), "enter")
	views["doctor"] = w.View()
	f.checks = nil
	w = press(t, w, "enter")
	views["lookup"] = w.View()
	views["picker"] = press(t, w, "n").View()
	a := press(t, w, "y")
	views["apps step"] = a.View()
	views["site step"] = press(t, a, "s").View()
	d := press(t, a, "enter")
	views["done"] = d.View()
	views["confirm"] = press(t, d, "b").View()

	f.probeRes = failedProbe()
	views["probe failed"] = press(t, prep(newModel(t, f)), "p").View()
	views["sites probe failed"] = goTo(t, press(t, prep(newModel(t, f)), "p"), pgSites).View()
	f.probeRes = nil
	f.foreign = []ops.Foreign{zapret2()}
	f.takeoverRep = takeoverReport()
	tk := prep(newModel(t, f))
	tk = settle(t, tk, tk.detectForeign())
	views["takeover notice"] = tk.View()
	views["takeover confirm"] = press(t, tk, "z").View()
	views["takeover report"] = press(t, press(t, tk, "z"), "y").View()
	f.foreign = []ops.Foreign{{Kind: "zapret", Running: true}}
	tk = prep(newModel(t, f))
	views["foreign manual"] = settle(t, tk, tk.detectForeign()).View()
	f.foreign = nil
	im := press(t, goTo(t, prep(newModel(t, f)), pgSites), "i")
	views["import prompt"] = im.View()
	f.importAdded, f.importSkipped = []string{"a.example"}, skippedLines(30)
	views["imported"] = press(t, typeText(t, im, "/tmp/list.txt"), "enter").View()

	f.cfg.DisclaimerAccepted = false
	views["disclaimer"] = prep(newModel(t, f)).View()
	f.cfg.DisclaimerAccepted = true

	f.st = supervisor.Status{State: "stopped"}
	views["status stopped"] = prep(newModel(t, f)).View()
	q := prep(newModel(t, f))
	q.running, q.confirmQuit = true, true
	views["quit prompt"] = q.View()
	f.st = supervisor.Status{State: "running", Mode: "background", PID: 4242}
	views["status background"] = prep(newModel(t, f)).View()
	return views
}

// Turkish strings are longer than English; every page must fit 80x24 in
// both, footer keys must stay next to their labels, and no key may be missing.
func TestPagesFit80x24(t *testing.T) {
	defer i18n.Init("en")
	for _, lang := range []string{"en", "tr", "ru"} {
		i18n.Init(lang)
		views := pageViews(t, func(m Model) Model { return m })
		for name, v := range views {
			lines := strings.Split(v, "\n")
			if len(lines) != 24 {
				t.Errorf("%s %s: %d lines, want 24:\n%s", lang, name, len(lines), v)
			}
			for _, l := range lines {
				if n := lipgloss.Width(l); n > 80 {
					t.Errorf("%s %s: %d columns: %q", lang, name, n, l)
				}
			}
			if k := missingKey.FindString(v); k != "" {
				t.Errorf("%s %s: missing key %s", lang, name, k)
			}
		}
		for k, label := range map[string]string{"s": "tui.act.stop", "p": "tui.act.probe", "q": "tui.act.quit"} {
			if !strings.Contains(views["status"], k+" "+i18n.T(label)) {
				t.Errorf("%s status: key %s is not next to its label %q", lang, k, i18n.T(label))
			}
		}
		// A wrapped value continues under the value column, not under the labels.
		f := newFake()
		f.cfg.DNSProvider = "auto"
		lines := strings.Split((statusPage{}).View(newModel(t, f), 60, 18), "\n")
		for i, l := range lines {
			if strings.HasPrefix(l, i18n.T("tui.dns")+" ") && i+1 < len(lines) && strings.TrimSpace(lines[i+1]) != "" {
				if !strings.HasPrefix(lines[i+1], strings.Repeat(" ", 12)) {
					t.Errorf("%s status: wrapped DNS value not indented: %q", lang, lines[i+1])
				}
			}
		}
	}
}

// The approved layout, at 80x24 in Turkish.
func TestSitesLayoutTurkish(t *testing.T) {
	defer i18n.Init("en")
	i18n.Init("tr")
	f := newFake()
	f.set = trSet()
	f.sites = []string{"pastebin.com"}
	f.st.State = "running"
	m := press(t, newModel(t, f), "down")
	lines := strings.Split(m.View(), "\n")
	for i, want := range []string{
		"◆ SplitDPI v0.1.0",
		"╭──────────────╮╭",
		"│   Durum      ││ Siteler  · engel aşmanın her zaman uygulandığı siteler",
		"│ ▌ Siteler    ││",
		"│   Strateji   ││   ✓ Discord       uygulama", // the menu has focus: one cursor
		"│   Tarama     ││   ✓ YouTube       uygulama",
		"│   Günlük     ││   ✓ pastebin.com  sizin eklediğiniz",
		"│   Ayarlar    ││",
	} {
		if !strings.HasPrefix(lines[i], want) {
			t.Errorf("line %d = %q, want prefix %q", i, lines[i], want)
		}
	}
	if !strings.HasSuffix(lines[0], " ● ÇALIŞIYOR ") {
		t.Errorf("header %q", lines[0])
	}
	if strings.TrimSpace(lines[20]) != "" {
		t.Errorf("toast line %q, want empty", lines[20])
	}
	has(t, lines[21], " a Site ekle ")
	has(t, lines[21], " e Bir site bozuldu ")
	if !strings.HasSuffix(lines[21], " ? Yardım   q Çık ") {
		t.Errorf("dock row 1 %q, want ? and q at the right", lines[21])
	}
	has(t, lines[22], " i Dosyadan al ")
	if lines[23] != "$ sudo splitdpi sites add DOMAIN" {
		t.Errorf("CLI line %q", lines[23])
	}
	if n := strings.Count(m.View(), "▸"); n != 0 {
		t.Errorf("%d list cursors with the menu focused, want 0", n)
	}
	m = press(t, m, "enter")
	if l := strings.Split(m.View(), "\n")[4]; !strings.HasPrefix(l, "│   Strateji   ││ ▸ ✓ Discord       uygulama") {
		t.Errorf("in the page, line 4 = %q; want the list cursor", l)
	}
}

// flat joins a page's words, so wrapped text reads as one line.
func flat(s string) string { return strings.Join(strings.Fields(s), " ") }

func skippedLines(n int) []ops.ImportSkip {
	out := make([]ops.ImportSkip, n)
	for i := range out {
		out[i] = ops.ImportSkip{Line: 2*i + 3, Text: "not a domain " + strconv.Itoa(i)}
	}
	return out
}

func failedProbe() map[string][]probe.Result {
	return map[string][]probe.Result{
		"discord": {{
			URL:   "https://discord.com/api/v9/gateway",
			Err:   "read tcp 10.0.0.2:51234->162.159.128.233:443: read: connection reset by peer",
			Cause: &net.OpError{Op: "read", Net: "tcp", Err: os.NewSyscallError("read", syscall.ECONNRESET)},
		}},
		"youtube": {{URL: "https://www.youtube.com/", OK: true}},
	}
}

func zapret2() ops.Foreign {
	return ops.Foreign{
		Kind:     "zapret2",
		Dir:      "/opt/zapret2",
		Service:  initsys.ForeignService{Init: sysinfo.Systemd, Name: "zapret2", Enabled: true},
		Hostlist: "/opt/zapret2/ipset/zapret-hosts-user.txt",
		Running:  true,
	}
}

func takeoverReport() ops.TakeoverReport {
	return ops.TakeoverReport{
		Added:    []string{"discord.gg", "pastebin.com"},
		Skipped:  []ops.ImportSkip{{Line: 4, Text: "1.2.3.4/24"}},
		Stopped:  true,
		Disabled: true,
	}
}

func TestSitesImport(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SUDO_USER", "")
	f := newFake()
	f.set = trSet()
	f.importAdded = []string{"pastebin.com", "rutracker.org"}
	f.importSkipped = []ops.ImportSkip{{Line: 3, Text: "not a domain!"}, {Line: 7, Text: "http://"}}
	m := goTo(t, newModel(t, f), pgSites)
	has(t, m.View(), "i From a file")
	m = press(t, m, "i")
	if !m.apps.typing || m.apps.ask != askFile {
		t.Fatalf("i: typing %v ask %v", m.apps.typing, m.apps.ask)
	}
	has(t, flat(m.View()), "Which file? One site per line")
	m = typeText(t, m, "~/list.txt")
	has(t, m.View(), "$ sudo splitdpi sites import ~/list.txt")
	m = press(t, m, "enter")
	path := filepath.Join(home, "list.txt")
	if f.count("sites import "+path) != 1 {
		t.Fatalf("calls %q", f.calls)
	}
	v := m.View()
	has(t, v, "Added 2 sites. Skipped 2 lines. Restart splitdpi to apply.")
	has(t, v, "rutracker.org")
	has(t, v, "Skipped lines, not a domain:")
	has(t, v, "line 3: not a domain!")
	has(t, v, "line 7: http://")
	if !slices.Equal(m.apps.sites, []string{"pastebin.com", "rutracker.org"}) {
		t.Errorf("sites %q", m.apps.sites)
	}
	has(t, m.logs.String(), path+":3: not a domain!")

	// A long list of skipped lines is cut short; the log has them all.
	f.importSkipped = skippedLines(40)
	m = press(t, typeText(t, press(t, m, "i"), "/tmp/big.txt"), "enter")
	has(t, m.View(), "line 3: not a domain 0")
	has(t, m.View(), "more; Logs lists them all")
	has(t, m.logs.String(), "/tmp/big.txt:81: not a domain 39")
	for _, l := range strings.Split(m.View(), "\n") {
		if lipgloss.Width(l) > 80 {
			t.Errorf("line %d wide: %q", lipgloss.Width(l), l)
		}
	}
}

func TestSitesImportCancelAndMissingFile(t *testing.T) {
	f := newFake()
	f.set = trSet()
	m := press(t, goTo(t, newModel(t, f), pgSites), "i")
	m = press(t, typeText(t, m, "/tmp/x.txt"), "esc")
	if m.apps.typing || slices.ContainsFunc(f.calls, func(c string) bool { return strings.HasPrefix(c, "sites import") }) {
		t.Fatalf("esc: typing %v calls %q", m.apps.typing, f.calls)
	}
	f.importAdded, f.importSkipped = []string{"one.example"}, []ops.ImportSkip{{Line: 2, Text: "?"}}
	m = press(t, typeText(t, press(t, m, "i"), "/tmp/one.txt"), "enter")
	has(t, m.View(), "Added 1 site. Skipped 1 line. Restart splitdpi to apply.")
	f.importErr = &fs.PathError{Op: "open", Path: "/tmp/none.txt", Err: fs.ErrNotExist}
	m = press(t, typeText(t, press(t, m, "i"), "/tmp/none.txt"), "enter")
	has(t, m.View(), "There is no file /tmp/none.txt.")
	has(t, m.logs.String(), "open /tmp/none.txt: file does not exist")
}

func TestExpandHome(t *testing.T) {
	t.Setenv("HOME", "/home/someone")
	t.Setenv("SUDO_USER", "")
	for in, want := range map[string]string{
		"~/a/sites.txt": "/home/someone/a/sites.txt",
		"/etc/x.txt":    "/etc/x.txt",
		"rel.txt":       "rel.txt",
		"~other/x":      "~other/x",
	} {
		if got := expandHome(in); got != want {
			t.Errorf("expandHome(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSitesAppNoteTurkish(t *testing.T) {
	defer i18n.Init("en")
	i18n.Init("tr")
	f := newFake()
	f.set = trSet()
	f.set.Apps["telegram"] = presets.App{ID: "telegram", Name: "Telegram", Note: "Direct connections to Telegram IPs are not covered."}
	m := goTo(t, newModel(t, f), pgSites)
	v := m.View()
	has(t, v, "Telegram  uygulama, Telegram'ın IP")
	if strings.Contains(v, "Direct connections") {
		t.Errorf("English note in tr:\n%s", v)
	}
	// The note wraps under the row instead of being cut.
	has(t, flat(pageText(v)), "Telegram'ın IP adreslerine doğrudan bağlantıları (MTProto) kapsanmıyor.")
}

func TestProbeFailureInPlainWords(t *testing.T) {
	f := newFake()
	f.set = trSet()
	f.probeRes = failedProbe()
	m := press(t, newModel(t, f), "p")
	page := flat(pages[pgStatus].View(m, 60, 18))
	has(t, page, "1/2 sites open · 1 blocked")
	has(t, page, "✗ Discord connection cut")
	has(t, page, "What to do Find another strategy on the Scan page.")
	if strings.Contains(m.View(), "connection reset by peer") {
		t.Errorf("raw error on the status page:\n%s", m.View())
	}
	has(t, m.logs.String(), "https://discord.com/api/v9/gateway: read tcp 10.0.0.2:51234->162.159.128.233:443: read: connection reset by peer")

	m = goTo(t, m, pgSites)
	has(t, m.View(), "Discord  app, "+probe.Reset.What()[:20])
	has(t, flat(pages[pgSites].View(m, 60, 18)), probeTodo(probe.Reset))
	if strings.Contains(m.View(), "connection reset") {
		t.Errorf("raw error on the sites page:\n%s", m.View())
	}
	// The hint follows the cursor: YouTube passed.
	if strings.Contains(flat(pages[pgSites].View(press(t, m, "down"), 60, 18)), probeTodo(probe.Reset)) {
		t.Error("what to do shown for a row that passed")
	}
}

func TestTakeoverNoticeAndConfirm(t *testing.T) {
	f := newFake()
	f.foreign = []ops.Foreign{zapret2()}
	f.takeoverRep = takeoverReport()
	m := newModel(t, f)
	m = settle(t, m, m.Init())
	page := flat(pages[pgStatus].View(m, 60, 18))
	has(t, page, "zapret2 is running on this computer. Use SplitDPI instead of it?")
	has(t, m.View(), "z Switch to SplitDPI")
	has(t, m.View(), "$ sudo splitdpi takeover --yes")

	m = press(t, m, "z")
	if m.dialog == nil {
		t.Fatal("z did not ask")
	}
	want := []string{
		"Add the sites listed in /opt/zapret2/ipset/zapret-hosts-user.txt to your sites.",
		"Stop the zapret2 service (systemd).",
		"Keep zapret2 from starting on boot.",
		"Leave its files alone: nothing is deleted or edited.",
	}
	if !slices.Equal(m.dialog.changes, want) || m.dialog.undo != "sudo splitdpi revert" {
		t.Fatalf("changes %q undo %q", m.dialog.changes, m.dialog.undo)
	}
	has(t, m.View(), "Use SplitDPI instead of zapret2?")
	has(t, m.View(), "sudo splitdpi revert")

	m = press(t, m, "n")
	if m.dialog != nil || f.count("takeover zapret2") != 0 {
		t.Fatalf("n: dialog %v calls %q", m.dialog, f.calls)
	}
	has(t, flat(pages[pgStatus].View(m, 60, 18)), "zapret2 is running")

	m = press(t, press(t, m, "z"), "y")
	if f.count("takeover zapret2") != 1 {
		t.Fatalf("y: calls %q", f.calls)
	}
	page = flat(pages[pgStatus].View(m, 120, 18))
	for _, s := range []string{
		"Switched to SplitDPI:",
		"added 2 new sites from /opt/zapret2/ipset/zapret-hosts-user.txt",
		"1 line of its list was not a domain and was skipped",
		"stopped the zapret2 service",
		"zapret2 no longer starts on boot",
	} {
		has(t, page, s)
	}
	if strings.Contains(page, "is running on this computer") || strings.Contains(m.View(), "z Switch") {
		t.Errorf("notice still shown after the takeover:\n%s", m.View())
	}
	has(t, m.View(), "Done. The other tool is off")
	has(t, m.logs.String(), "/opt/zapret2/ipset/zapret-hosts-user.txt:4: 1.2.3.4/24")
}

// The longest confirm on Durum fits 80x24 in Turkish down to its keys.
func TestTakeoverConfirmFitsTurkish(t *testing.T) {
	defer i18n.Init("en")
	i18n.Init("tr")
	f := newFake()
	f.foreign = []ops.Foreign{zapret2()}
	m := newModel(t, f)
	m = press(t, settle(t, m, m.Init()), "z")
	v := m.View()
	for _, want := range []string{
		"zapret2 yerine SplitDPI kullanılsın mı?",
		"zapret2 servisinin açılışta başlaması kapatılacak.",
		"dokunulmayacak: hiçbir şey silinmeyecek",
		"ya da değiştirilmeyecek.",
		"sudo splitdpi revert",
		"e Evet, devam et   h Vazgeç",
	} {
		has(t, v, want)
	}
	for _, l := range strings.Split(v, "\n") {
		if lipgloss.Width(l) > 80 {
			t.Errorf("%d columns: %q", lipgloss.Width(l), l)
		}
	}
	m = press(t, m, "h")
	if m.dialog != nil || f.count("takeover zapret2") != 0 {
		t.Fatalf("h: dialog %v calls %q", m.dialog, f.calls)
	}
	press(t, press(t, m, "z"), "e")
	if f.count("takeover zapret2") != 1 {
		t.Errorf("e: calls %q", f.calls)
	}
}

func TestTakeoverManualStop(t *testing.T) {
	f := newFake()
	f.foreign = []ops.Foreign{{Kind: "zapret", Running: true}}
	m := newModel(t, f)
	m = settle(t, m, m.Init())
	has(t, flat(pages[pgStatus].View(m, 60, 18)), "zapret is running on this computer, but not as a service, so SplitDPI cannot stop it. Stop it yourself")
	if strings.Contains(m.View(), "z Switch") {
		t.Error("z offered for a process without a service")
	}
	if m = press(t, m, "z"); m.dialog != nil {
		t.Error("z asked to take over a process without a service")
	}
}

func TestNoTakeoverNotice(t *testing.T) {
	for name, found := range map[string][]ops.Foreign{
		"none": nil,
		// Installed but neither running nor starting on boot: not in the way.
		"dormant": {{Kind: "zapret2", Dir: "/opt/zapret2", Hostlist: "/opt/zapret2/ipset/zapret-hosts-user.txt", Service: initsys.ForeignService{Init: sysinfo.Systemd, Name: "zapret2"}}},
	} {
		f := newFake()
		f.foreign = found
		m := newModel(t, f)
		m = settle(t, m, m.Init())
		if v := m.View(); strings.Contains(v, "zapret") || strings.Contains(v, "z Switch") {
			t.Errorf("%s: notice shown:\n%s", name, v)
		}
	}
	// Coming back to Durum looks again.
	f := newFake()
	m := goTo(t, goTo(t, newModel(t, f), pgSites), pgStatus)
	if f.count("detect foreign") != 1 {
		t.Errorf("calls %q", f.calls)
	}
	f.foreign = []ops.Foreign{zapret2()}
	m = goTo(t, goTo(t, m, pgSites), pgStatus)
	has(t, m.View(), "z Switch to SplitDPI")
}
