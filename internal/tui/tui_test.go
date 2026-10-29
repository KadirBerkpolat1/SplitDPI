package tui

import (
	"context"
	"io"
	"os"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/KadirBerkpolat1/SplitDPI/internal/config"
	"github.com/KadirBerkpolat1/SplitDPI/internal/doctor"
	"github.com/KadirBerkpolat1/SplitDPI/internal/i18n"
	"github.com/KadirBerkpolat1/SplitDPI/internal/ops"
	"github.com/KadirBerkpolat1/SplitDPI/internal/presets"
	"github.com/KadirBerkpolat1/SplitDPI/internal/probe"
	"github.com/KadirBerkpolat1/SplitDPI/internal/scan"
	"github.com/KadirBerkpolat1/SplitDPI/internal/supervisor"
)

func TestMain(m *testing.M) {
	i18n.Init("en")
	os.Exit(m.Run())
}

// fake is the backend the TUI drives; each method records or returns canned data.
type fake struct {
	mu       sync.Mutex
	st       supervisor.Status
	cfg      config.Config
	runCtx   context.Context
	runStart chan struct{}
	logs     string
	calls    []string
	checks   []doctor.Check
	set      presets.Set
	detected scan.ASNInfo
	scanFn     func(ctx context.Context, opt scan.Options, progress func(string)) (scan.Report, error)
	excluded   []string
	sites      []string
	strategies []ops.StrategyChoice
	ipv6       bool
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
	for _, c := range f.calls {
		if c == s {
			return true
		}
	}
	return false
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
	return nil
}
func (f *fake) Stop() error { f.record("stop"); return nil }
func (f *fake) Logs(ctx context.Context, follow bool, w io.Writer) error {
	io.WriteString(w, f.logs)
	<-ctx.Done()
	return nil
}
func (f *fake) Probe(ctx context.Context, apps []string) (map[string][]probe.Result, error) {
	f.record("probe")
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

func newModel(t *testing.T, f *fake) Model {
	t.Helper()
	m := New(context.Background(), f)
	m.every = func(time.Duration, func(time.Time) tea.Msg) tea.Cmd { return nil }
	m.width, m.height = 80, 24
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
	has(t, v, "sudo splitdpi run")
}

func TestFitsIn80x24(t *testing.T) {
	f := newFake()
	f.cfg.Strategy = &config.Strategy{ID: "tt-multidisorder", Source: "isp/tr/as9121-turk-telekom.toml"}
	f.st.LastError = strings.Repeat("a very long error message ", 10)
	v := newModel(t, f).View()
	lines := strings.Split(v, "\n")
	if len(lines) > 24 {
		t.Errorf("%d lines, want ≤ 24", len(lines))
	}
	for _, l := range lines {
		if w := len([]rune(l)); w > 80 {
			t.Errorf("line %d runes wide: %q", w, l)
		}
	}
}

func TestLongTextWrapsInsteadOfCut(t *testing.T) {
	f := newFake()
	m := press(t, newModel(t, f), "w")
	v := m.View()
	if !strings.Contains(strings.Join(strings.Fields(v), " "), "This sends one request to that site.") {
		t.Errorf("lookup question cut off:\n%s", v)
	}
}

func TestFocusMovesFooterCommand(t *testing.T) {
	m := newModel(t, newFake())
	m, _ = update(t, m, key("tab"))
	has(t, m.View(), "sudo splitdpi scan")
}

func TestLogsScreen(t *testing.T) {
	f := newFake()
	f.logs = "nfqws2 started (pid 42)\n"
	m := newModel(t, f)
	m, cmd := update(t, m, key("l"))
	if m.scr != scrLogs {
		t.Fatalf("screen %v, want logs", m.scr)
	}
	has(t, m.View(), "sudo splitdpi logs -f")
	go settle(t, m, cmd) // the log stream blocks until the screen is left
	deadline := time.Now().Add(2 * time.Second)
	for !strings.Contains(m.logs.String(), "nfqws2 started") && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	m, _ = update(t, m, logTickMsg{})
	has(t, m.View(), "nfqws2 started (pid 42)")
	m, _ = update(t, m, key("esc"))
	if m.scr != scrStatus {
		t.Errorf("esc: screen %v, want status", m.scr)
	}
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
	has(t, m.View(), "discord")
	has(t, m.View(), "reachable")
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
	m, cmd = update(t, m, key("q"))
	if cmd != nil {
		if _, ok := cmd().(tea.QuitMsg); ok {
			t.Fatal("quit without asking while the bypass runs")
		}
	}
	has(t, m.View(), "Stop bypass and quit?")
	m, cmd = update(t, m, key("y"))
	if f.runCtx.Err() == nil {
		t.Error("run not cancelled")
	}
	<-done
	m = settle(t, m, cmd)
	_ = m
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
	if strings.Contains(m.View(), "none, run setup") {
		t.Error("status shown before the disclaimer was accepted")
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
	has(t, m.View(), "sudo apt install nftables")
	m = press(t, m, "enter") // check again
	if m.scr != scrWizard || m.wiz.step != wDoctor {
		t.Errorf("left the doctor step with a blocker: screen %v step %v", m.scr, m.wiz.step)
	}
	has(t, m.View(), "sudo splitdpi doctor")
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
	if m.scr != scrWizard {
		t.Errorf("esc during the scan left the wizard: %v", m.scr)
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
	if !f.called("enable") {
		t.Error("start at boot did not enable the service")
	}
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
	if m.scr != scrWizard || m.wiz.step != wApps {
		t.Fatalf("esc on the site step: screen %v step %v, want the apps step", m.scr, m.wiz.step)
	}
	f.ipv6 = false
	m = press(t, m, "s")
	has(t, m.View(), "only IPv4")
	for _, r := range "https://Pastebin.com/" {
		m, _ = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
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
	m := press(t, newModel(t, f), "a")
	has(t, m.View(), "Discord")
	m = press(t, m, "b")
	for _, r := range "https://Bank.example/" {
		m, _ = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	has(t, m.View(), "sudo splitdpi exclude add https://Bank.example/")
	m = press(t, m, "enter")
	if !f.called("exclude add https://Bank.example/") {
		t.Fatalf("calls %q", f.calls)
	}
	has(t, m.View(), "bank.example")
}

func TestAppsToggle(t *testing.T) {
	f := newFake()
	f.set = trSet()
	f.cfg.Apps = []string{"discord"}
	m := press(t, newModel(t, f), "a")
	has(t, m.View(), "sudo splitdpi apps disable discord")
	m = press(t, m, "space")
	if !f.called("apps disable discord") {
		t.Errorf("calls %q", f.calls)
	}
}

func TestAppsAddRemoveSite(t *testing.T) {
	f := newFake()
	f.set = trSet()
	m := press(t, newModel(t, f), "a")
	has(t, m.View(), "Your sites")
	has(t, m.View(), "sudo splitdpi apps disable discord")
	m = press(t, m, "a")
	for _, r := range "https://Pastebin.com/" {
		m, _ = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	has(t, m.View(), "sudo splitdpi sites add https://Pastebin.com/")
	m = press(t, m, "enter")
	if !f.called("sites add https://Pastebin.com/") {
		t.Fatalf("calls %q", f.calls)
	}
	has(t, m.View(), "pastebin.com")
	// The cursor runs over the two apps, then the site.
	m = press(t, press(t, m, "down"), "down")
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
	m := newModel(t, f)
	m, _ = update(t, m, key("tab"))
	m, _ = update(t, m, key("tab"))
	has(t, m.View(), "sudo splitdpi strategy list")
	m = press(t, m, "t")
	has(t, m.View(), "Türk Telekom")
	has(t, m.View(), "[x] tt-multidisorder")
	has(t, m.View(), "sudo splitdpi strategy set tt-multidisorder")
	m = press(t, m, "down")
	has(t, m.View(), "sudo splitdpi strategy set generic-multidisorder")
	m = press(t, m, "enter")
	if !f.called("strategy set generic-multidisorder") {
		t.Fatalf("calls %q", f.calls)
	}
	has(t, m.View(), "Strategy set to generic-multidisorder")
	has(t, m.View(), "[x] generic-multidisorder")
}

// Turkish and Russian strings are longer than English; every screen must still
// fit 80x24 and show no missing keys.
func TestLocalesFit80x24(t *testing.T) {
	defer i18n.Init("en")
	for _, lang := range []string{"tr", "ru"} {
		i18n.Init(lang)
		f := newFake()
		f.set = trSet()
		f.sites = []string{"pastebin.com"}
		f.strategies = testStrategies()
		f.cfg.Strategy = &config.Strategy{ID: "tt-multidisorder", Source: "isp/tr/as9121-turk-telekom.toml"}
		f.cfg.DNS = true
		f.st = supervisor.Status{State: "running", Mode: "service", PID: 4242, Engine: "v1.0.6", LastError: "nfqws2 stopped (exit status 1)"}
		f.detected = scan.ASNInfo{ASN: 9121, Country: "TR", Org: "Turk Telekom"}
		views := map[string]string{}
		m := newModel(t, f)
		m = press(t, m, "p")
		views["status"] = m.View()
		views["logs"] = func() string { m, _ := update(t, m, key("l")); return m.View() }()
		w := press(t, m, "w")
		views["lookup"] = w.View()
		views["picker"] = press(t, w, "n").View()
		views["apps step"] = press(t, w, "y").View()
		views["site step"] = press(t, press(t, w, "y"), "s").View()
		a := press(t, press(t, m, "a"), "b")
		views["site broke"] = a.View()
		views["add site"] = press(t, press(t, m, "a"), "a").View()
		views["strategy"] = press(t, m, "t").View()
		f.cfg.DisclaimerAccepted = false
		views["disclaimer"] = newModel(t, f).View()
		for name, v := range views {
			lines := strings.Split(v, "\n")
			if len(lines) > 24 {
				t.Errorf("%s %s: %d lines", lang, name, len(lines))
			}
			for _, l := range lines {
				if n := len([]rune(l)); n > 80 {
					t.Errorf("%s %s: %d columns: %q", lang, name, n, l)
				}
			}
			if k := missingKey.FindString(v); k != "" {
				t.Errorf("%s %s: missing key %s", lang, name, k)
			}
			for _, l := range lines {
				if strings.HasSuffix(strings.TrimRight(l, " "), "]") {
					t.Errorf("%s %s: a key is split from its label: %q", lang, name, l)
				}
			}
		}
		// A wrapped value continues under the value column, not under the labels.
		lines := strings.Split(views["status"], "\n")
		for i, l := range lines {
			if strings.HasPrefix(l, i18n.T("tui.dns")+" ") && i+1 < len(lines) && strings.TrimSpace(lines[i+1]) != "" {
				if !strings.HasPrefix(lines[i+1], strings.Repeat(" ", 12)) {
					t.Errorf("%s status: wrapped DNS value not indented: %q", lang, lines[i+1])
				}
			}
		}
	}
}
