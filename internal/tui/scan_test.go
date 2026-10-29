package tui

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"net/netip"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/KadirBerkpolat1/SplitDPI/internal/config"
	"github.com/KadirBerkpolat1/SplitDPI/internal/doctor"
	"github.com/KadirBerkpolat1/SplitDPI/internal/engine"
	"github.com/KadirBerkpolat1/SplitDPI/internal/i18n"
	"github.com/KadirBerkpolat1/SplitDPI/internal/ops"
	"github.com/KadirBerkpolat1/SplitDPI/internal/probe"
	"github.com/KadirBerkpolat1/SplitDPI/internal/scan"
)

var engineBlocker = doctor.Check{ID: "engine", Severity: doctor.Blocker, Msg: "the zapret2 engine is not installed", Fix: "sudo splitdpi engine install"}

func TestScanPageOffersBothFlows(t *testing.T) {
	f := newFake()
	f.set = trSet()
	f.detected = scan.ASNInfo{ASN: 9121, Country: "TR", Org: "Turk Telekom"}
	m := goTo(t, newModel(t, f), pgScan)
	has(t, m.View(), "▸ "+i18n.T("tui.wiz.scan"))
	has(t, m.View(), i18n.T("tui.wiz.scan_site"))
	has(t, m.View(), "$ sudo splitdpi scan")
	m = press(t, m, "down")
	has(t, m.View(), "▸ "+i18n.T("tui.wiz.scan_site"))
	has(t, m.View(), "$ sudo splitdpi scan --site DOMAIN")
	if f.called("doctor") {
		t.Fatal("moving between the flows started one")
	}

	// The site flow goes from the ISP straight to the site: no apps step.
	m = press(t, press(t, m, "enter"), "y")
	if m.wiz.step != wSite || !m.wiz.siteFlow {
		t.Fatalf("site flow after the lookup: step %v siteFlow %v", m.wiz.step, m.wiz.siteFlow)
	}
	m = press(t, m, "esc")
	if m.wiz.step != wIntro || m.page != pgScan || !m.inPage {
		t.Fatalf("esc on the site: step %v page %d inPage %v; want the flows", m.wiz.step, m.page, m.inPage)
	}

	var got scan.Options
	f.scanFn = func(_ context.Context, opt scan.Options, _ func(string)) (scan.Report, error) {
		got = opt
		return scan.Report{}, scan.ErrNoStrategy
	}
	m = press(t, press(t, m, "s"), "y")
	m = typeText(t, m, "pastebin.com")
	m = press(t, m, "enter")
	if got.Site != "pastebin.com" || len(got.Apps) != 0 || f.called("apps enable discord") {
		t.Fatalf("site flow scan %+v, calls %q; want the site with the configured apps untouched", got, f.calls)
	}

	// Back at the flows, the last scan is summed up.
	m = press(t, m, "b")
	if m.wiz.step != wIntro {
		t.Fatalf("start over: step %v", m.wiz.step)
	}
	has(t, m.View(), i18n.T("scan_page.last"))
	has(t, m.View(), i18n.N("scan_page.last_none", 0))
	has(t, m.View(), "pastebin.com")
}

func TestScanPageShowsSavedStrategy(t *testing.T) {
	f := newFake()
	f.cfg.Strategy = &config.Strategy{ID: "tt-multidisorder", Source: "isp/tr/as9121-turk-telekom.toml"}
	m := goTo(t, newModel(t, f), pgScan)
	has(t, m.View(), "tt-multidisorder")
	if strings.Contains(m.View(), i18n.T("scan_page.last")) {
		t.Error("a last scan shown before any scan ran")
	}
}

func TestWizardInstallsMissingEngine(t *testing.T) {
	f := newFake()
	f.checks = []doctor.Check{engineBlocker}
	m := press(t, newModel(t, f), "w")
	has(t, m.View(), i18n.T("scan_page.install"))
	has(t, m.View(), "$ sudo splitdpi engine install")
	if f.called("engine install ") {
		t.Fatal("installed before being asked")
	}
	m, cmd := update(t, m, key("enter"))
	has(t, m.View(), i18n.T("scan_page.installing", engine.Version))
	m = settle(t, m, cmd)
	if !f.called("engine install ") {
		t.Fatalf("enter did not install the engine: %q", f.calls)
	}
	if f.count("doctor") != 2 || m.wiz.step != wLookup {
		t.Fatalf("after installing: step %v, %d doctor runs; want the checks rerun and passed", m.wiz.step, f.count("doctor"))
	}
	has(t, m.View(), i18n.T("engine.installed", engine.Version, "/opt/splitdpi/zapret2-"+engine.Version))
	if !strings.Contains(m.logs.String(), engine.URL) {
		t.Errorf("the download is not in the log: %q", m.logs.String())
	}
}

func TestWizardEngineInstallFailureShowsHint(t *testing.T) {
	f := newFake()
	f.checks = []doctor.Check{engineBlocker}
	f.engineErr = errors.New("dial tcp: lookup github.com: no such host")
	m := press(t, press(t, newModel(t, f), "w"), "enter")
	if m.wiz.step != wDoctor {
		t.Fatalf("step %v after a failed install", m.wiz.step)
	}
	v := m.View()
	has(t, v, "Engine install failed: dial tcp")
	has(t, v, "If the download is blocked")
	has(t, v, "$ sudo splitdpi engine install --from FILE")
	if !strings.Contains(m.logs.String(), "no such host") {
		t.Errorf("raw error not logged: %q", m.logs.String())
	}
	// r checks again; enter tries the download again.
	m = press(t, m, "r")
	if f.count("doctor") != 2 || f.count("engine install ") != 1 {
		t.Errorf("r: calls %q", f.calls)
	}
}

// Other blockers still block once the engine is in.
func TestWizardEngineInstalledOtherBlockerRemains(t *testing.T) {
	f := newFake()
	nft := doctor.Check{ID: "nft", Severity: doctor.Blocker, Msg: "nft is not installed", Fix: "sudo apt install nftables"}
	f.checks = []doctor.Check{nft, engineBlocker}
	m := press(t, press(t, newModel(t, f), "w"), "enter")
	if !f.called("engine install ") || m.wiz.step != wDoctor {
		t.Fatalf("step %v, calls %q", m.wiz.step, f.calls)
	}
	has(t, m.View(), "nft is not installed")
	if strings.Contains(m.View(), i18n.T("scan_page.install")) {
		t.Error("install still offered with the engine in place")
	}
	has(t, m.View(), "$ sudo splitdpi doctor")
}

func TestScanTrialFailureText(t *testing.T) {
	f := newFake()
	started := make(chan struct{})
	f.scanFn = func(ctx context.Context, opt scan.Options, progress func(string)) (scan.Report, error) {
		tr := scan.Trial{Results: []probe.Result{
			{URL: "https://www.youtube.com/", OK: true},
			{URL: "https://discord.com/api/v9/gateway", Err: "read tcp 10.0.0.2:5555->162.159.135.232:443: read: connection reset by peer", Cause: syscall.ECONNRESET},
		}}
		tr.Candidate.Strategy.ID = "tt-multidisorder"
		opt.Trial(tr)
		close(started)
		<-ctx.Done()
		return scan.Report{Trials: []scan.Trial{tr}}, scan.ErrNoStrategy
	}
	m, cmd := toScan(t, f)
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	<-started
	want := "    discord.com: " + probe.Reset.What()
	check := func(when string) {
		if !strings.Contains(m.wiz.progress.String(), "✗ tt-multidisorder\n"+want) {
			t.Errorf("%s: trial lines %q, want %q", when, m.wiz.progress.String(), want)
		}
		has(t, m.View(), "✗ tt-multidisorder")
		has(t, m.View(), "discord.com: ")
		if strings.Contains(m.View(), "connection reset") || strings.Contains(m.View(), "youtube") {
			t.Errorf("%s: raw error or a passing probe in the view:\n%s", when, m.View())
		}
	}
	check("during the scan")
	m.wiz.scanCancel()
	m, _ = update(t, m, <-done)
	check("after the scan")
	if !strings.Contains(m.logs.String(), "connection reset by peer") {
		t.Errorf("raw error not logged: %q", m.logs.String())
	}
}

func TestScanShareLinkAfterWinner(t *testing.T) {
	defer i18n.Init("en")
	for _, lang := range []string{"en", "tr"} {
		i18n.Init(lang)
		f := newFake()
		rep := scan.Report{
			ASN: scan.ASNInfo{ASN: 9121, Country: "TR", Org: "Turk Telekom"},
			Winner: &config.Strategy{ID: "blockcheck", Source: "blockcheck2", Date: "2026-10-29", Engine: engine.Version,
				TCPTLS: "--payload=tls_client_hello --lua-desync=multidisorder:pos=1,midsld --lua-desync=fake:blob=fake_default_tls:repeats=6"},
		}
		f.scanFn = func(context.Context, scan.Options, func(string)) (scan.Report, error) { return rep, nil }
		m, cmd := toScan(t, f)
		m = settle(t, m, cmd)
		if m.wiz.step != wDone {
			t.Fatalf("%s: step %v", lang, m.wiz.step)
		}
		u := ops.ShareURL(rep, "v0.1.0")
		v := m.View()
		has(t, v, i18n.T("scan_page.share")[:20])
		for _, chunk := range strings.Split(ansi.Hardwrap(u, 60, false), "\n") {
			if !strings.Contains(v, chunk) {
				t.Errorf("%s: link cut, missing %q:\n%s", lang, chunk, v)
			}
		}
		fits(t, lang+" done", v)
	}
}

func TestScanCopyLinkAndPrintOnExit(t *testing.T) {
	f := newFake()
	rep := scan.Report{
		ASN:    scan.ASNInfo{ASN: 9121, Country: "TR", Org: "Turk Telekom"},
		Winner: &config.Strategy{ID: "tt-multidisorder", Source: "isp/tr/as9121-turk-telekom.toml"},
	}
	f.scanFn = func(context.Context, scan.Options, func(string)) (scan.Report, error) { return rep, nil }
	m, cmd := toScan(t, f)
	var out bytes.Buffer
	m.out = &out
	var none bytes.Buffer
	m.printShare(&none)
	if none.Len() != 0 {
		t.Fatalf("printed %q before any strategy was saved", none.String())
	}
	m = settle(t, m, cmd)
	u := ops.ShareURL(rep, "v0.1.0")
	if !slices.Contains(m.logs.Last(50), u) {
		t.Errorf("the link is not one line of the log: %q", m.logs.String())
	}
	has(t, m.View(), "c "+i18n.T("scan_page.copy"))

	m = press(t, m, "c")
	if want := "\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte(u)) + "\a"; out.String() != want {
		t.Errorf("c wrote %q, want the OSC 52 sequence %q", out.String(), want)
	}
	has(t, m.View(), i18n.T("scan_page.copied"))

	// The link outlives the result screen and is printed once on exit.
	m = press(t, m, "b")
	m = press(t, m, "n")
	m.resetWizard()
	var exit bytes.Buffer
	m.printShare(&exit)
	if exit.String() != u+"\n" {
		t.Errorf("exit printed %q, want the link once on its own line", exit.String())
	}
}

// dnsScan starts a scan that asks the DNS question and returns the running
// scan command's result and the answer the scan got.
func dnsScan(t *testing.T, f *fake) (Model, chan tea.Msg, chan string) {
	t.Helper()
	answer := make(chan string, 1)
	f.scanFn = func(ctx context.Context, opt scan.Options, progress func(string)) (scan.Report, error) {
		answer <- opt.DNSCheck(ctx, []probe.DNSResult{
			{Domain: "discord.com", Poisoned: true, System: []netip.Addr{netip.MustParseAddr("195.175.254.2")}, DoH: []netip.Addr{netip.MustParseAddr("162.159.135.232")}},
		})
		<-ctx.Done()
		return scan.Report{}, ctx.Err()
	}
	m, cmd := toScan(t, f)
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	return m, done, answer
}

// waitAsk ticks the scan until its DNS question shows.
func waitAsk(t *testing.T, m Model) Model {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); m.ask == nil; time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the DNS question never showed")
		}
		m, _ = update(t, m, scanTickMsg{})
	}
	return m
}

func recv(t *testing.T, c chan string) string {
	t.Helper()
	select {
	case s := <-c:
		return s
	case <-time.After(5 * time.Second):
		t.Fatal("the scan is still waiting for its DNS answer")
	}
	return ""
}

func TestScanDNSQuestion(t *testing.T) {
	for _, tc := range []struct {
		name string
		keys []string
		want string
	}{
		{"recommended", []string{"enter"}, "cloudflare"},
		{"yes key", []string{"y"}, "cloudflare"},
		{"evet key", []string{"e"}, "cloudflare"},
		{"hayır key", []string{"h"}, ""},
		{"other provider", []string{"down", "enter", "down", "down", "down", "enter"}, "quad9"},
		{"other then back", []string{"down", "enter", "esc", "down", "enter"}, ""},
		{"no", []string{"n"}, ""},
		{"esc", []string{"esc"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFake()
			m, done, answer := dnsScan(t, f)
			m = waitAsk(t, m)
			v := m.View()
			has(t, v, i18n.T("scan_page.dns.title"))
			has(t, v, "discord.com")
			has(t, v, i18n.T("scan_page.dns.yes"))
			has(t, v, "sudo splitdpi dns off")
			if !strings.Contains(m.logs.String(), "195.175.254.2") {
				t.Errorf("poisoned addresses not logged: %q", m.logs.String())
			}
			for _, k := range tc.keys {
				m = press(t, m, k)
			}
			if m.ask != nil {
				t.Fatal("the dialog stayed open after an answer")
			}
			if got := recv(t, answer); got != tc.want {
				t.Errorf("scan got %q, want %q", got, tc.want)
			}
			if !m.wiz.scanning || m.page != pgScan {
				t.Errorf("answering stopped the scan or left the page: scanning %v page %d", m.wiz.scanning, m.page)
			}
			m.cancelScan()
			<-done
		})
	}
}

func TestScanDNSQuestionProviderList(t *testing.T) {
	f := newFake()
	m, done, answer := dnsScan(t, f)
	m = press(t, press(t, waitAsk(t, m), "down"), "enter")
	v := m.View()
	has(t, v, i18n.T("scan_page.dns.pick"))
	has(t, v, "▸ "+i18n.T("dns2.auto_name"))
	has(t, flat(v), strings.Fields(i18n.T("dns2.auto_note"))[0])
	has(t, v, "AdGuard")
	for _, p := range m.ask.providers {
		if p.ID == "off" {
			t.Error("off offered as a provider")
		}
	}
	fits(t, "dns providers", v)
	m = press(t, m, "enter")
	if got := recv(t, answer); got != "auto" {
		t.Errorf("scan got %q, want auto", got)
	}
	m.cancelScan()
	<-done
}

// A cancelled scan or a quitting TUI frees the waiting scan with "".
func TestScanDNSQuestionCancel(t *testing.T) {
	t.Run("quit with the dialog open", func(t *testing.T) {
		f := newFake()
		m, done, answer := dnsScan(t, f)
		m = waitAsk(t, m)
		m, _ = update(t, m, tea.KeyMsg{Type: tea.KeyCtrlC})
		if got := recv(t, answer); got != "" {
			t.Errorf("scan got %q", got)
		}
		m, _ = update(t, m, <-done)
		if m.ask != nil {
			t.Error("the dialog outlived the scan")
		}
	})
	t.Run("esc before the dialog shows", func(t *testing.T) {
		f := newFake()
		m, done, answer := dnsScan(t, f)
		m, _ = update(t, m, key("esc"))
		if got := recv(t, answer); got != "" {
			t.Errorf("scan got %q", got)
		}
		m, _ = update(t, m, <-done)
		m, _ = update(t, m, scanTickMsg{})
		if m.ask != nil || !m.wiz.cancelled {
			t.Errorf("ask %v cancelled %v", m.ask, m.wiz.cancelled)
		}
	})
}

// Durum's Setup while a scan runs (reached by clicking the menu) shows that
// scan instead of starting over and orphaning it.
func TestSetupDuringScanKeepsIt(t *testing.T) {
	f := newFake()
	started := make(chan context.Context, 1)
	f.scanFn = func(ctx context.Context, _ scan.Options, _ func(string)) (scan.Report, error) {
		started <- ctx
		<-ctx.Done()
		return scan.Report{}, ctx.Err()
	}
	m, cmd := toScan(t, f)
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	scanCtx := <-started
	m, _ = update(t, m, tea.MouseMsg{X: 5, Y: menuTop + pgStatus, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if m.page != pgStatus {
		t.Fatalf("click on Durum: page %d", m.page)
	}
	m, _ = update(t, m, key("w"))
	if m.page != pgScan || !m.wiz.scanning || m.wiz.step != wScan || scanCtx.Err() != nil {
		t.Fatalf("w during a scan: page %d scanning %v step %v cancelled %v", m.page, m.wiz.scanning, m.wiz.step, scanCtx.Err())
	}
	m, _ = update(t, m, key("esc"))
	if scanCtx.Err() == nil {
		t.Fatal("esc no longer cancels the scan")
	}
	m, _ = update(t, m, <-done)
	has(t, m.View(), "cancelled")
}

// Durum's Setup during an engine download shows that download instead of
// starting over, so a second download cannot be started next to it.
func TestSetupDuringEngineInstallKeepsIt(t *testing.T) {
	f := newFake()
	f.checks = []doctor.Check{engineBlocker}
	m := press(t, newModel(t, f), "w")
	m, install := update(t, m, key("enter"))
	if !m.wiz.installing || install == nil {
		t.Fatalf("enter did not start the install: installing %v", m.wiz.installing)
	}
	m, _ = update(t, m, tea.MouseMsg{X: 5, Y: menuTop + pgStatus, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	m = press(t, press(t, m, "w"), "enter")
	if m.page != pgScan || !m.wiz.installing {
		t.Fatalf("w during the install: page %d installing %v", m.page, m.wiz.installing)
	}
	m = settle(t, m, install)
	if n := f.count("engine install "); n != 1 {
		t.Errorf("%d engine installs, want 1: %q", n, f.calls)
	}
	if m.wiz.installing || m.wiz.step != wLookup {
		t.Errorf("after the install: installing %v step %v", m.wiz.installing, m.wiz.step)
	}
}

// fits checks a screen is exactly 80x24 with every key translated.
func fits(t *testing.T, name, v string) {
	t.Helper()
	lines := strings.Split(v, "\n")
	if len(lines) != 24 {
		t.Errorf("%s: %d lines, want 24:\n%s", name, len(lines), v)
	}
	for _, l := range lines {
		if n := lipgloss.Width(l); n > 80 {
			t.Errorf("%s: %d columns: %q", name, n, l)
		}
	}
	if k := missingKey.FindString(v); k != "" {
		t.Errorf("%s: missing key %s", name, k)
	}
}

func TestScanScreensFit80x24(t *testing.T) {
	defer i18n.Init("en")
	for _, lang := range []string{"en", "tr", "ru"} {
		i18n.Init(lang)
		f := newFake()
		f.cfg.Strategy = &config.Strategy{ID: "tt-multidisorder", Source: "isp/tr/as9121-turk-telekom.toml"}
		m := goTo(t, newModel(t, f), pgScan)
		m.wiz.last = &lastScan{rep: scan.Report{Trials: make([]scan.Trial, 3), Winner: &config.Strategy{ID: "blockcheck"}}, site: "pastebin.com"}
		fits(t, lang+" flows", m.View())

		f.checks = []doctor.Check{engineBlocker}
		d := press(t, m, "enter")
		fits(t, lang+" engine offer", d.View())
		installing, _ := update(t, d, key("enter"))
		fits(t, lang+" installing", installing.View())
		f.engineErr = errors.New("dial tcp: lookup github.com: no such host")
		fits(t, lang+" install failed", press(t, d, "enter").View())

		f.checks, f.engineErr, f.cfg.Strategy = nil, nil, nil
		dm, done, answer := dnsScan(t, f)
		dm = waitAsk(t, dm)
		v := dm.View()
		fits(t, lang+" dns question", v)
		has(t, v, i18n.T("scan_page.dns.ok"))
		dm = press(t, press(t, dm, "down"), "enter")
		fits(t, lang+" dns providers", dm.View())
		has(t, dm.View(), i18n.T("scan_page.dns.ok"))
		dm = press(t, dm, "enter")
		recv(t, answer)
		writeTrial(dm.wiz.progress, dm.logs, dm.g, scan.Trial{Results: []probe.Result{{URL: "https://discord.com/", Cause: errors.New("x")}}})
		fits(t, lang+" scan trials", dm.View())
		dm.cancelScan()
		<-done
	}
}
