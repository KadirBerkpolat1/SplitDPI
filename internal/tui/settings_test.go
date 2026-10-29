package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/KadirBerkpolat1/SplitDPI/internal/i18n"
	"github.com/KadirBerkpolat1/SplitDPI/internal/ops"
)

const (
	rowDNS = iota + 1
	rowUninstall
)

// toSetting opens the settings page with the cursor on row.
func toSetting(t *testing.T, f *fake, row int) Model {
	t.Helper()
	m := goTo(t, newModel(t, f), pgSettings)
	for m.settings.cursor < row {
		m = press(t, m, "down")
	}
	return m
}

func (f *fake) count(s string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if c == s {
			n++
		}
	}
	return n
}

func (f *fake) dnsCalled() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if strings.HasPrefix(c, "dns ") {
			return true
		}
	}
	return false
}

func footerCLI(m Model) string {
	lines := strings.Split(m.View(), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.HasPrefix(lines[i], "$ ") {
			return strings.TrimPrefix(lines[i], "$ ")
		}
	}
	return ""
}

func TestSettingsDNSPickerListsProviders(t *testing.T) {
	f := newFake()
	m := toSetting(t, f, rowDNS)
	has(t, m.View(), "Encrypted DNS")
	has(t, m.View(), "Off")
	if cli := footerCLI(m); cli != "sudo splitdpi dns <id> [--dot]" {
		t.Errorf("row CLI %q", cli)
	}
	m = press(t, m, "enter")
	if m.settings.dns == nil {
		t.Fatal("enter did not open the picker")
	}
	v := m.View()
	has(t, v, "Protocol: DNS-over-HTTPS")
	ps := f.DNSProviders()
	if len(ps) != 6 {
		t.Fatalf("%d providers", len(ps))
	}
	for _, p := range ps {
		has(t, v, p.Name)
		has(t, v, p.Note[:min(len(p.Note), 20)])
	}
	has(t, v, "Also blocks ads and trackers.")
	has(t, v, "Also blocks known malware")
	has(t, v, "t Use DNS-over-TLS")
	if cli := footerCLI(m); cli != "sudo splitdpi dns off" {
		t.Errorf("picker CLI %q", cli)
	}
	m = press(t, m, "q")
	m = press(t, m, "esc")
	if m.settings.dns != nil || m.quitting || f.dnsCalled() {
		t.Fatalf("esc: picker %v, quitting %v, calls %q", m.settings.dns, m.quitting, f.calls)
	}
}

func TestSettingsDNSConfirm(t *testing.T) {
	f := newFake()
	m := press(t, toSetting(t, f, rowDNS), "enter")
	m = press(t, press(t, m, "down"), "down")
	if cli := footerCLI(m); cli != "sudo splitdpi dns cloudflare" {
		t.Fatalf("CLI %q", cli)
	}
	m = press(t, m, "enter")
	if m.dialog == nil {
		t.Fatal("choosing cloudflare did not ask")
	}
	want := "While SplitDPI runs, DNS queries go to Cloudflare over DNS-over-HTTPS. The system DNS settings are not touched."
	if m.dialog.changes[0] != want || m.dialog.undo != "sudo splitdpi dns off" {
		t.Errorf("dialog %q, undo %q", m.dialog.changes, m.dialog.undo)
	}
	v := m.View()
	has(t, v, "Turn on encrypted DNS?")
	has(t, v, "sudo splitdpi dns off")
	m = press(t, m, "n")
	if m.dialog != nil || f.dnsCalled() {
		t.Fatalf("n: dialog %v, calls %q", m.dialog, f.calls)
	}
	m = press(t, press(t, m, "enter"), "y")
	if f.count("dns cloudflare doh") != 1 {
		t.Fatalf("calls %q", f.calls)
	}
	if m.settings.dns != nil {
		t.Error("picker still open after setting")
	}
	v = m.View()
	has(t, v, "Encrypted DNS is on: Cloudflare over DNS-over-HTTPS.")
	has(t, v, "Cloudflare (DNS-over-HTTPS)")
	if strings.Contains(v, "restart it to apply") {
		t.Error("restart note while stopped")
	}
}

func TestSettingsDNSDoTWhileRunning(t *testing.T) {
	f := newFake()
	f.st.State = "running"
	m := press(t, toSetting(t, f, rowDNS), "enter")
	for range 4 {
		m = press(t, m, "down")
	}
	m = press(t, m, "t")
	v := m.View()
	has(t, v, "Protocol: DNS-over-TLS")
	has(t, v, "port 853")
	has(t, v, "t Use DNS-over-HTTPS")
	if cli := footerCLI(m); cli != "sudo splitdpi dns quad9 --dot" {
		t.Fatalf("CLI %q", cli)
	}
	m = press(t, m, "enter")
	if m.dialog == nil {
		t.Fatal("no confirm")
	}
	ch := strings.Join(m.dialog.changes, "\n")
	for _, want := range []string{"Quad9 over DNS-over-TLS", i18n.T("dns2.note_quad9"), i18n.T("settings_dns.on_restart")} {
		if !strings.Contains(ch, want) {
			t.Errorf("changes lack %q: %q", want, ch)
		}
	}
	m = press(t, m, "y")
	if f.count("dns quad9 dot") != 1 {
		t.Fatalf("calls %q", f.calls)
	}
	has(t, m.View(), "restart it to apply")
}

func TestSettingsDNSOffWithoutConfirm(t *testing.T) {
	f := newFake()
	f.cfg.DNSProvider = "cloudflare"
	m := press(t, toSetting(t, f, rowDNS), "enter")
	if p := m.settings.dns; p.list[p.cursor].ID != "cloudflare" {
		t.Fatalf("picker starts on %s", p.list[p.cursor].ID)
	}
	m = press(t, press(t, m, "up"), "up")
	m = press(t, m, "enter")
	if m.dialog != nil {
		t.Fatal("turning DNS off asked")
	}
	if f.count("dns off doh") != 1 {
		t.Fatalf("calls %q", f.calls)
	}
	has(t, m.View(), i18n.T("dns.off"))
}

func uninstallFake() *fake {
	f := newFake()
	f.plan = ops.UninstallPlan{Service: "splitdpi service (systemd)", Paths: []string{
		"/etc/splitdpi", "/var/lib/splitdpi", "/run/splitdpi", "/var/log/splitdpi.log", "/var/log/splitdpi.log.1", "/usr/local/bin/splitdpi",
	}}
	f.uninstallRep = ops.UninstallReport{Removed: append([]string{"splitdpi service (systemd)", "nft table inet splitdpi"}, f.plan.Paths...)}
	return f
}

func TestSettingsUninstall(t *testing.T) {
	f := uninstallFake()
	m := toSetting(t, f, rowUninstall)
	has(t, m.View(), "Uninstall SplitDPI")
	if cli := footerCLI(m); cli != "sudo splitdpi uninstall --yes" {
		t.Fatalf("CLI %q", cli)
	}
	m = press(t, m, "enter")
	if m.dialog == nil {
		t.Fatal("no confirm")
	}
	v := m.View()
	has(t, v, "Uninstall SplitDPI?")
	has(t, v, "splitdpi service (systemd)")
	for _, p := range f.plan.Paths {
		has(t, v, p)
	}
	has(t, v, "zapret service")
	has(t, v, "This cannot be undone")
	has(t, v, repoURL)
	has(t, v, "y "+i18n.T("shell.confirm.yes"))
	m = press(t, m, "n")
	if m.dialog != nil || f.called("uninstall") {
		t.Fatalf("n: dialog %v, calls %q", m.dialog, f.calls)
	}
	m = press(t, press(t, m, "enter"), "y")
	if n := f.count("uninstall"); n != 1 {
		t.Fatalf("uninstall called %d times", n)
	}
	if !m.settings.uninstalled {
		t.Fatal("not marked uninstalled")
	}
	v = m.View()
	has(t, v, "SplitDPI is uninstalled.")
	has(t, v, "Removed /usr/local/bin/splitdpi")
	has(t, v, "Press any key")
	_, cmd := update(t, m, key("x"))
	if cmd == nil {
		t.Fatal("a key after uninstall does not quit")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("a key after uninstall does not quit")
	}
}

func TestSettingsUninstallIncomplete(t *testing.T) {
	f := uninstallFake()
	f.uninstallRep = ops.UninstallReport{Removed: []string{"/run/splitdpi"}, Leftover: []string{"/etc/splitdpi", "/usr/local/bin/splitdpi"}}
	f.uninstallErr = ops.ErrUninstallIncomplete
	m := press(t, press(t, toSetting(t, f, rowUninstall), "enter"), "y")
	if m.settings.uninstalled {
		t.Fatal("incomplete uninstall marked done")
	}
	v := m.View()
	has(t, v, "Removed /run/splitdpi")
	has(t, v, "Still present: /etc/splitdpi")
	has(t, v, "Still present: /usr/local/bin/splitdpi")
	has(t, v, "Uninstall did not finish")
	if m = press(t, m, "up"); m.settings.cursor != rowDNS {
		t.Errorf("page stuck after incomplete uninstall: cursor %d", m.settings.cursor)
	}
}

func TestSettingsUninstallNothing(t *testing.T) {
	f := newFake()
	m := press(t, toSetting(t, f, rowUninstall), "enter")
	if m.dialog != nil || f.called("uninstall") {
		t.Fatal("asked with nothing to remove")
	}
	has(t, m.View(), i18n.T("uninstall.nothing"))
}

func TestSettingsFit80x24(t *testing.T) {
	defer i18n.Init("en")
	for _, lang := range []string{"en", "tr", "ru"} {
		i18n.Init(lang)
		f := uninstallFake()
		f.st.State = "running"
		f.cfg.DNSProvider = "auto"
		views := map[string]string{}
		m := goTo(t, newModel(t, f), pgSettings)
		views["rows"] = m.View()
		p := press(t, press(t, m, "down"), "enter")
		views["picker"] = p.View()
		p = press(t, press(t, p, "down"), "t")
		views["picker dot"] = p.View()
		views["dns confirm"] = press(t, p, "enter").View()
		u := press(t, press(t, press(t, m, "down"), "down"), "enter")
		views["uninstall confirm"] = u.View()
		views["uninstalled"] = press(t, u, "y").View()
		f.uninstallErr = ops.ErrUninstallIncomplete
		f.uninstallRep.Leftover = f.plan.Paths
		views["incomplete"] = press(t, u, "y").View()
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
		for _, name := range []string{"dns confirm", "uninstall confirm"} {
			if !strings.Contains(views[name], "n "+i18n.T("shell.confirm.no")) {
				t.Errorf("%s %s: the y/n line is cut off:\n%s", lang, name, views[name])
			}
		}
	}
}
