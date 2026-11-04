package tui

import (
	"errors"
	"strings"
	"testing"

	"github.com/KadirBerkpolat1/SplitDPI/internal/doctor"
	"github.com/KadirBerkpolat1/SplitDPI/internal/i18n"
	"github.com/KadirBerkpolat1/SplitDPI/internal/ops"
	"github.com/KadirBerkpolat1/SplitDPI/internal/sysinfo"
)

var (
	nftBlocker = doctor.Check{ID: "nft", Severity: doctor.Blocker, Msg: "nft is not installed", Fix: "sudo apt install nftables"}
	curlWarn   = doctor.Check{ID: "curl", Severity: doctor.Warn, Msg: "curl is not installed", Fix: "sudo apt install curl"}
	lookupWarn = doctor.Check{ID: "lookup", Severity: doctor.Warn, Msg: "nslookup and host are not installed", Fix: "sudo apt install bind9-host"}
)

func aptPkgs(tools ...sysinfo.Tool) sysinfo.Packages { return sysinfo.NewPackages(sysinfo.Apt, tools) }

func TestWizardInstallsMissingPackages(t *testing.T) {
	f := newFake()
	f.checks = []doctor.Check{curlWarn, lookupWarn}
	f.pkgs = aptPkgs(sysinfo.Curl, sysinfo.Lookup)
	m := press(t, newModel(t, f), "w")
	if m.wiz.step != wDoctor {
		t.Fatalf("step %v with packages missing; want the checks", m.wiz.step)
	}
	v := m.View()
	has(t, v, i18n.T("tui.pkgs.head"))
	has(t, v, "curl is not installed")
	has(t, v, i18n.T("tui.pkgs.missing", "curl, bind9-host"))
	has(t, v, i18n.T("tui.pkgs.install"))
	has(t, v, i18n.T("tui.pkgs.skip"))
	has(t, v, "$ sudo splitdpi deps install")
	if strings.Contains(v, i18n.T("tui.wiz.doctor")) {
		t.Error("nothing blocks, yet the checks say they must be fixed first")
	}
	if strings.Contains(v, "sudo apt install curl") {
		t.Errorf("the package summary and the fix line both list curl:\n%s", v)
	}

	m = press(t, m, "enter")
	if m.dialog == nil || f.called("install curl bind9-host") {
		t.Fatalf("enter: dialog %v, calls %q; want a question first", m.dialog, f.calls)
	}
	v = m.View()
	has(t, v, i18n.T("tui.pkgs.title"))
	has(t, v, i18n.T("tui.pkgs.change", "curl, bind9-host", "apt"))
	has(t, v, "apt-get install -y curl bind9-host")
	has(t, v, i18n.T("tui.pkgs.undo"))
	has(t, v, "sudo apt remove curl bind9-host")

	m, cmd := update(t, m, key("e"))
	has(t, m.View(), i18n.T("deps.installing", "curl, bind9-host", "apt"))
	m = settle(t, m, cmd)
	if !f.called("install curl bind9-host") {
		t.Fatalf("yes did not install: %q", f.calls)
	}
	if f.count("doctor") != 2 || m.wiz.step != wLookup {
		t.Fatalf("after installing: step %v, %d doctor runs; want the checks rerun and passed", m.wiz.step, f.count("doctor"))
	}
	has(t, m.View(), i18n.T("deps.installed", "curl, bind9-host"))
}

func TestWizardContinuesWithoutPackages(t *testing.T) {
	f := newFake()
	f.checks = []doctor.Check{curlWarn}
	f.pkgs = aptPkgs(sysinfo.Curl)
	m := press(t, press(t, newModel(t, f), "w"), "c")
	if m.wiz.step != wLookup || f.called("install curl") {
		t.Fatalf("c: step %v, calls %q; want the ISP without installing", m.wiz.step, f.calls)
	}

	// The checks rerun in the same setup: the packages are not offered again.
	m.wiz.step = wDoctor
	cmd := m.runDoctor()
	m = settle(t, m, cmd)
	if f.count("doctor") != 2 || m.wiz.step != wLookup || m.dialog != nil {
		t.Fatalf("rerun: step %v, %d doctor runs, dialog %v; want the ISP again", m.wiz.step, f.count("doctor"), m.dialog)
	}
}

func TestWizardOffersPackagesForBlocker(t *testing.T) {
	f := newFake()
	f.checks = []doctor.Check{nftBlocker, engineBlocker}
	f.pkgs = aptPkgs(sysinfo.Nft)
	m := press(t, newModel(t, f), "w")
	v := m.View()
	has(t, v, i18n.T("tui.wiz.doctor"))
	has(t, v, i18n.T("tui.pkgs.install"))
	has(t, v, i18n.T("tui.pkgs.missing", "nftables"))
	if strings.Contains(v, i18n.T("tui.pkgs.skip")) || strings.Contains(v, i18n.T("scan_page.install")) {
		t.Errorf("nft blocks: offered going on without it, or the engine before the packages:\n%s", v)
	}
	if m = press(t, m, "c"); m.wiz.step != wDoctor || m.wiz.pkgsSkipped {
		t.Fatalf("c went on past a blocker: step %v", m.wiz.step)
	}

	// Once the packages are in, the checks rerun and the engine is offered.
	m = press(t, press(t, m, "enter"), "y")
	if !f.called("install nftables") || m.wiz.step != wDoctor {
		t.Fatalf("step %v, calls %q", m.wiz.step, f.calls)
	}
	has(t, m.View(), i18n.T("scan_page.install"))
	has(t, m.View(), "$ sudo splitdpi engine install")
}

func TestWizardPackageInstallFailureShowsHint(t *testing.T) {
	f := newFake()
	f.checks = []doctor.Check{curlWarn}
	f.pkgs = aptPkgs(sysinfo.Curl)
	f.pkgsErr = ops.InstallError{Err: errors.New("apt-get: exit status 100"), Hint: "sudo apt install curl"}
	m := press(t, press(t, press(t, newModel(t, f), "w"), "enter"), "y")
	if !f.called("install curl") || m.wiz.step != wDoctor || f.count("doctor") != 1 {
		t.Fatalf("after a failed install: step %v, calls %q; want the checks kept", m.wiz.step, f.calls)
	}
	v := m.View()
	has(t, v, i18n.T("tui.pkgs.failed", "apt-get: exit status 100"))
	if strings.Contains(v, "Could not install the packages") {
		t.Errorf("the failure is worded twice:\n%s", v)
	}
	has(t, v, i18n.T("deps.hint", "sudo apt install curl"))
	if !strings.Contains(m.logs.String(), "exit status 100") {
		t.Errorf("error not logged: %q", m.logs.String())
	}
	// enter asks again.
	if m = press(t, m, "enter"); m.dialog == nil {
		t.Error("enter after a failure does not offer the install again")
	}
}

func TestWizardUnknownPackageManager(t *testing.T) {
	f := newFake()
	f.checks = []doctor.Check{curlWarn}
	f.pkgs = sysinfo.NewPackages(sysinfo.Unknown, []sysinfo.Tool{sysinfo.Curl})
	if m := press(t, newModel(t, f), "w"); m.wiz.step != wLookup {
		t.Fatalf("a warning stopped the setup: step %v", m.wiz.step)
	}

	f.checks = []doctor.Check{nftBlocker}
	f.pkgs = sysinfo.NewPackages(sysinfo.Unknown, []sysinfo.Tool{sysinfo.Nft})
	m := press(t, newModel(t, f), "w")
	v := m.View()
	if m.wiz.step != wDoctor || strings.Contains(v, i18n.T("tui.pkgs.install")) {
		t.Fatalf("step %v; want the checks without an install:\n%s", m.wiz.step, v)
	}
	has(t, v, "$ sudo apt install nftables")
	has(t, v, "$ sudo splitdpi doctor")
}

func TestPackageScreensFit80x24(t *testing.T) {
	defer i18n.Init("en")
	for _, lang := range []string{"en", "tr", "ru"} {
		i18n.Init(lang)
		f := newFake()
		f.checks = []doctor.Check{nftBlocker, curlWarn, lookupWarn, engineBlocker}
		f.pkgs = aptPkgs(sysinfo.Nft, sysinfo.Curl, sysinfo.Lookup)
		m := goTo(t, newModel(t, f), pgScan)
		d := press(t, m, "enter")
		fits(t, lang+" packages offer", d.View())
		d = press(t, d, "enter")
		fits(t, lang+" packages question", d.View())
		installing, _ := update(t, d, key("y"))
		fits(t, lang+" packages installing", installing.View())
		f.pkgsErr = ops.InstallError{Err: errors.New("apt-get: exit status 100: E: Unable to locate package bind9-host"), Hint: "sudo apt install nftables curl bind9-host"}
		fits(t, lang+" packages failed", press(t, d, "y").View())
	}
}
