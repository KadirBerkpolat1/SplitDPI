package tui

import (
	"errors"
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/KadirBerkpolat1/SplitDPI/internal/config"
	"github.com/KadirBerkpolat1/SplitDPI/internal/doctor"
	"github.com/KadirBerkpolat1/SplitDPI/internal/engine"
	"github.com/KadirBerkpolat1/SplitDPI/internal/i18n"
	"github.com/KadirBerkpolat1/SplitDPI/internal/ops"
)

// fullList is the settings list drawn tall enough for every row.
func fullList(m Model) string { return ansi.Strip(settingsPage{}.View(m, 60, 100)) }

func shownIDs(m Model) []string {
	var ids []string
	for _, st := range m.shownSettings() {
		ids = append(ids, st.id)
	}
	return ids
}

func TestSettingsGroupsInOrder(t *testing.T) {
	defer i18n.Init("en")
	want := [][]string{
		{"lang", "run_mode"},
		{"mode", "exclude", "strategy"},
		{"dns"},
		{"skip_ifaces", "ipinfo_url"},
		{"presets", "engine", "doctor", "uninstall"},
		{"qnum"},
	}
	for _, lang := range []string{"en", "tr"} {
		i18n.Init(lang)
		m := goTo(t, newModel(t, newFake()), pgSettings)
		v := fullList(m)
		at := -1
		for g, ids := range want {
			h := strings.Index(v, groupName(g)+"\n")
			if h <= at {
				t.Fatalf("%s: group %s not after the one before:\n%s", lang, groupName(g), v)
			}
			at = h
			for _, id := range ids {
				i := slices.IndexFunc(settings, func(s setting) bool { return s.id == id })
				r := strings.Index(v[at:], settings[i].label())
				if r < 0 {
					t.Fatalf("%s: %s not after %s:\n%s", lang, settings[i].label(), groupName(g), v)
				}
				at += r
			}
		}
		if got := shownIDs(m); slices.Contains(got, "takeover") || slices.Contains(got, "revert") {
			t.Errorf("%s: takeover or revert shown on a clean system: %q", lang, got)
		}
	}
}

func TestSettingsRowValues(t *testing.T) {
	f := newFake()
	f.excluded = []string{"bank.example", "gov.example"}
	f.set.Version = "2026.10.01"
	f.engineDir = "/opt/splitdpi/zapret2-" + engine.Version
	m := goTo(t, newModel(t, f), pgSettings)
	v := fullList(m)
	for _, want := range []string{
		"Scope                      Every site",
		"Excluded sites             2 sites",
		"Strategy                   none",
		"Interfaces to leave alone  wg*, tun*, tailscale*",
		"Provider lookup service    https://ipinfo.io/json",
		"Update presets             version 2026.10.01",
		"Install/update engine      zapret2 " + engine.Version,
		"Check the system           not checked yet",
		"Queue number               200",
	} {
		has(t, v, want)
	}
}

// clearInput empties the focused input.
func clearInput(t *testing.T, m Model) Model {
	t.Helper()
	for range 60 {
		m, _ = update(t, m, tea.KeyMsg{Type: tea.KeyBackspace})
	}
	return m
}

func TestSettingsSearch(t *testing.T) {
	f := newFake()
	m := press(t, goTo(t, newModel(t, f), pgSettings), "/")
	if !m.settings.searching {
		t.Fatal("/ did not open the search")
	}
	m = typeText(t, m, "dnsq")
	if !slices.Equal(shownIDs(m), nil) {
		t.Errorf("dnsq shows %q", shownIDs(m))
	}
	has(t, m.View(), "No setting matches.")
	m, _ = update(t, m, tea.KeyMsg{Type: tea.KeyBackspace})
	if got := shownIDs(m); !slices.Equal(got, []string{"dns"}) {
		t.Fatalf("dns shows %q", got)
	}
	v := m.View()
	has(t, v, "/ dns")
	has(t, v, "Encrypted DNS")
	if strings.Contains(v, "Language") {
		t.Errorf("unmatched rows shown:\n%s", v)
	}
	// q is typed, not quit; a group name matches its rows; so does a value.
	m = typeText(t, clearInput(t, m), "network")
	if got := shownIDs(m); !slices.Equal(got, []string{"skip_ifaces", "ipinfo_url"}) || m.quitting {
		t.Errorf("network shows %q, quitting %v", got, m.quitting)
	}
	m = typeText(t, clearInput(t, m), "TAILSCALE")
	if got := shownIDs(m); !slices.Equal(got, []string{"skip_ifaces"}) {
		t.Errorf("TAILSCALE shows %q", got)
	}

	// enter keeps the filter and gives the keys to the rows; esc clears it
	// without leaving the page.
	m = typeText(t, clearInput(t, m), "dns")
	m = press(t, m, "enter")
	if m.settings.searching || !slices.Equal(shownIDs(m), []string{"dns"}) {
		t.Fatalf("enter: searching %v, rows %q", m.settings.searching, shownIDs(m))
	}
	has(t, m.View(), "Search: dns")
	if cli := footerCLI(m); cli != "sudo splitdpi dns <id> [--dot]" {
		t.Errorf("filtered row CLI %q", cli)
	}
	m = press(t, m, "esc")
	if !m.inPage || m.settings.search.Value() != "" || len(shownIDs(m)) != len(settings)-2 {
		t.Errorf("esc: in page %v, query %q, %d rows", m.inPage, m.settings.search.Value(), len(shownIDs(m)))
	}

	// esc in the box clears it too.
	m = press(t, typeText(t, press(t, m, "/"), "lang"), "esc")
	if m.settings.searching || m.settings.search.Value() != "" || !m.inPage {
		t.Errorf("esc in the box: searching %v, query %q", m.settings.searching, m.settings.search.Value())
	}

	// / from the menu gives the keys to the box.
	m = typeText(t, press(t, press(t, m, "esc"), "/"), "dns")
	if !m.inPage || !slices.Equal(shownIDs(m), []string{"dns"}) {
		t.Errorf("/ from the menu: in page %v, rows %q", m.inPage, shownIDs(m))
	}
}

// i, ı, I and İ are one letter to the search, in any language.
func TestSettingsSearchDotlessI(t *testing.T) {
	defer i18n.Init("en")
	i18n.Init("tr")
	m := goTo(t, newModel(t, newFake()), pgSettings)
	for q, want := range map[string]string{
		"KALDIR":    "uninstall", // kaldır
		"kaldir":    "uninstall",
		"DİL":       "lang",
		"dıl":       "lang",
		"LAYICI":    "ipinfo_url", // Sağlayıcıyı
		"sağlayıcı": "ipinfo_url",
		"kuyruk":    "qnum",
	} {
		s := typeText(t, press(t, m, "/"), q)
		if got := shownIDs(s); !slices.Contains(got, want) {
			t.Errorf("%q shows %q, want %s", q, got, want)
		}
	}
	if fold("İIıi") != "iiii" {
		t.Errorf("fold(İIıi) = %q", fold("İIıi"))
	}
}

func TestSettingsModePicker(t *testing.T) {
	f := newFake()
	m := toSetting(t, f, "mode")
	if cli := footerCLI(m); cli != "sudo splitdpi config set mode broad" {
		t.Errorf("row CLI %q", cli)
	}
	m = press(t, m, "enter")
	v := flat(pageText(m.View()))
	has(t, v, "Which traffic should the bypass handle?")
	has(t, v, i18n.T("settings_mode.broad_note"))
	has(t, v, i18n.T("settings_mode.apps_note"))
	m = press(t, m, "down")
	if cli := footerCLI(m); cli != "sudo splitdpi config set mode apps" {
		t.Errorf("picker CLI %q", cli)
	}
	m = press(t, m, "enter")
	if !f.called("mode apps") || m.settings.pick != nil {
		t.Fatalf("calls %q, picker %v", f.calls, m.settings.pick)
	}
	v = flat(m.View())
	has(t, v, "Saved. Scope: Only apps.")
	has(t, v, "Scope Only apps")
	if strings.Contains(v, "restarts") {
		t.Error("restart note while stopped")
	}

	f.st.State = "running"
	m = settle(t, m, m.refresh())
	m = press(t, press(t, press(t, m, "enter"), "up"), "enter")
	has(t, flat(m.View()), "Saved. Scope: Every site. "+i18n.T("settings_dns.on_restart"))
}

func TestSettingsModeAppsRefused(t *testing.T) {
	f := newFake()
	f.cfg.Apps = nil
	m := press(t, press(t, press(t, toSetting(t, f, "mode"), "enter"), "down"), "enter")
	if !f.called("mode apps") {
		t.Fatalf("calls %q", f.calls)
	}
	if m.settings.pick == nil {
		t.Fatal("the refusal closed the picker")
	}
	has(t, flat(pageText(m.View())), i18n.T("settings_mode.empty"))
	if f.cfg.Mode != config.ModeBroad || m.cfg.Mode == config.ModeApps {
		t.Errorf("mode changed to %s", f.cfg.Mode)
	}
	m = press(t, m, "esc")
	if m.settings.pick != nil {
		t.Error("esc did not close the picker")
	}
}

func TestSettingsLangPicker(t *testing.T) {
	defer i18n.Init("en")
	f := newFake()
	m := press(t, toSetting(t, f, "lang"), "enter")
	if p := m.settings.pick; p == nil || p.id != "lang" || p.cursor != 0 {
		t.Fatalf("picker %+v", p)
	}
	has(t, m.View(), "Türkçe")
	m = press(t, m, "down")
	if cli := footerCLI(m); cli != "sudo splitdpi config set lang tr" {
		t.Errorf("CLI %q", cli)
	}
	m = press(t, m, "enter")
	if i18n.Lang() != "tr" || !f.called("lang tr") || m.settings.pick != nil {
		t.Fatalf("lang %s, calls %q", i18n.Lang(), f.calls)
	}
	has(t, m.View(), "Dil kaydedildi.")
}

func TestSettingsQNumEditor(t *testing.T) {
	f := newFake()
	m := press(t, toSetting(t, f, "qnum"), "enter")
	if m.settings.text == nil || m.settings.text.id != "qnum" {
		t.Fatalf("editor %+v", m.settings.text)
	}
	v := flat(pageText(m.View()))
	has(t, v, i18n.T("settings_qnum.warn"))
	has(t, v, "Default: 200")

	m = press(t, typeText(t, clearInput(t, m), "abc"), "enter")
	if slices.ContainsFunc(f.calls, func(c string) bool { return strings.HasPrefix(c, "qnum") }) {
		t.Fatalf("not a number was passed on: %q", f.calls)
	}
	has(t, m.View(), "Enter a number from 0 to 65535.")

	m = typeText(t, clearInput(t, m), "70000")
	if cli := footerCLI(m); cli != "sudo splitdpi config set qnum 70000" {
		t.Errorf("CLI %q", cli)
	}
	m = press(t, m, "enter")
	if !f.called("qnum 70000") || m.settings.text == nil {
		t.Fatalf("calls %q, editor %v", f.calls, m.settings.text)
	}
	has(t, m.View(), "queue number 70000 is not between 0 and 65535")

	m = press(t, typeText(t, clearInput(t, m), "300"), "enter")
	if !f.called("qnum 300") || m.settings.text != nil {
		t.Fatalf("calls %q, editor %v", f.calls, m.settings.text)
	}
	v = flat(m.View())
	has(t, v, "Saved. Queue number: 300.")
	has(t, v, "Queue number 300")
}

func TestSettingsIPInfoEditor(t *testing.T) {
	f := newFake()
	m := press(t, toSetting(t, f, "ipinfo_url"), "enter")
	has(t, m.View(), "https://ipinfo.io/json")
	m = press(t, typeText(t, clearInput(t, m), "http://example.com"), "enter")
	if !f.called("ipinfo_url http://example.com") || m.settings.text == nil {
		t.Fatalf("calls %q, editor %v", f.calls, m.settings.text)
	}
	has(t, m.View(), "not an https URL: http://example.com")

	m = typeText(t, clearInput(t, m), "https://api.example.com/json")
	m = press(t, m, "esc")
	if m.settings.text != nil || f.called("ipinfo_url https://api.example.com/json") {
		t.Fatalf("esc saved: %q", f.calls)
	}

	m = clearInput(t, press(t, m, "enter"))
	if cli := footerCLI(m); cli != "sudo splitdpi config reset ipinfo_url" {
		t.Errorf("empty CLI %q", cli)
	}
	m = press(t, m, "enter")
	if !f.called("ipinfo_url ") || m.settings.text != nil {
		t.Fatalf("calls %q", f.calls)
	}
	has(t, flat(m.View()), "Saved. Provider lookup service: https://ipinfo.io/json.")
}

func TestSettingsIfaceEditor(t *testing.T) {
	f := newFake()
	m := press(t, toSetting(t, f, "skip_ifaces"), "enter")
	if m.settings.ifaces == nil {
		t.Fatal("no editor")
	}
	v := m.View()
	for _, i := range []string{"wg*", "tun*", "tailscale*"} {
		has(t, v, i)
	}
	if cli := footerCLI(m); cli != "sudo splitdpi config set skip_ifaces 'wg*,tun*,tailscale*,NAME'" {
		t.Errorf("CLI %q", cli)
	}
	if strings.Contains(v, "Restore defaults") {
		t.Error("restore offered for the defaults")
	}

	m = press(t, m, "x")
	if !f.called("skip_ifaces tun*,tailscale*") || !slices.Equal(m.settings.ifaces.list, []string{"tun*", "tailscale*"}) {
		t.Fatalf("calls %q, list %q", f.calls, m.settings.ifaces.list)
	}
	has(t, flat(m.View()), "Saved. Interfaces to leave alone: tun*, tailscale*.")

	m = press(t, typeText(t, press(t, m, "a"), "wg0"), "enter")
	if !f.called("skip_ifaces tun*,tailscale*,wg0") || m.settings.ifaces.adding {
		t.Fatalf("calls %q", f.calls)
	}
	m = press(t, typeText(t, press(t, m, "a"), "bad name"), "enter")
	if m.settings.ifaces == nil || !m.settings.ifaces.adding {
		t.Fatal("a refused name closed the input")
	}
	has(t, m.View(), "invalid interface name bad name")
	if slices.Contains(m.cfg.SkipIfaces, "bad name") {
		t.Error("refused name saved")
	}
	m = press(t, press(t, m, "esc"), "d")
	if !f.called("skip_ifaces wg*,tun*,tailscale*") {
		t.Fatalf("calls %q", f.calls)
	}
	m = press(t, m, "esc")
	if m.settings.ifaces != nil {
		t.Fatal("esc did not close")
	}
	has(t, fullList(m), "wg*, tun*, tailscale*")

	// Removing the last one leaves an empty list, which is allowed.
	f.cfg.SkipIfaces = []string{"wg*"}
	m = press(t, press(t, toSetting(t, f, "skip_ifaces"), "enter"), "x")
	if !f.called("skip_ifaces ") {
		t.Fatalf("calls %q", f.calls)
	}
	has(t, m.View(), i18n.T("settings_ifaces.empty"))
}

func TestShellArg(t *testing.T) {
	for in, want := range map[string]string{
		"https://ipinfo.io/json":         "https://ipinfo.io/json",
		"wg*,tun*":                       "'wg*,tun*'",
		"":                               "''",
		"https://x.example/?a=1&b=2":     "'https://x.example/?a=1&b=2'",
		"it's":                           `'it'\''s'`,
		"https://user:pw@x.example:8443": "https://user:pw@x.example:8443",
	} {
		if got := shellArg(in); got != want {
			t.Errorf("shellArg(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestSettingsShortcuts(t *testing.T) {
	f := newFake()
	f.set = trSet()
	f.sites = []string{"pastebin.com"}
	f.excluded = []string{"bank.example"}
	f.strategies = testStrategies()
	m := toSetting(t, f, "exclude")
	if cli := footerCLI(m); cli != "sudo splitdpi exclude list" {
		t.Errorf("CLI %q", cli)
	}
	m = press(t, m, "enter")
	if m.page != pgSites || !m.inPage {
		t.Fatalf("on page %d, in page %v", m.page, m.inPage)
	}
	v := m.View()
	has(t, v, "bank.example")
	has(t, flat(v), i18n.T("settings_exclude.jumped"))

	m = press(t, toSetting(t, f, "strategy"), "enter")
	if m.page != pgStrategy || !m.inPage || len(m.strat.list) == 0 {
		t.Fatalf("on page %d, in page %v, %d strategies", m.page, m.inPage, len(m.strat.list))
	}
}

func TestSettingsPresetsUpdate(t *testing.T) {
	f := newFake()
	m := toSetting(t, f, "presets")
	if cli := footerCLI(m); cli != "sudo splitdpi presets update" {
		t.Errorf("CLI %q", cli)
	}
	m, cmd := update(t, m, key("enter"))
	has(t, fullList(m), "Update presets             in progress...")
	m = settle(t, m, cmd)
	if !f.called("presets update") {
		t.Fatalf("calls %q", f.calls)
	}
	v := fullList(m)
	has(t, v, "version 2026.10.30")
	has(t, flat(v), "Updated preset cache to version 2026.10.30 (12 apps).")

	f.presetsErr = errors.New("fetch failed: 503")
	has(t, fullList(press(t, m, "enter")), "fetch failed: 503")
}

func TestSettingsEngineInstall(t *testing.T) {
	f := newFake()
	m := toSetting(t, f, "engine")
	has(t, fullList(m), "not installed")
	has(t, m.View(), "Enter Install")
	if cli := footerCLI(m); cli != "sudo splitdpi engine install" {
		t.Errorf("CLI %q", cli)
	}
	f.engineErr = engine.ErrChecksum
	m = press(t, m, "enter")
	v := flat(fullList(m))
	has(t, v, flat(installErrText(engine.ErrChecksum)))
	has(t, v, "not installed")

	f.engineErr = nil
	m = press(t, m, "enter")
	if f.count("engine install ") != 2 {
		t.Fatalf("calls %q", f.calls)
	}
	v = flat(fullList(m))
	has(t, v, "Install/update engine zapret2 "+engine.Version)
	has(t, v, "Installed zapret2 "+engine.Version)
	has(t, m.View(), "Enter Reinstall")
}

func TestSettingsDoctor(t *testing.T) {
	f := newFake()
	f.checks = []doctor.Check{
		{ID: "root", Severity: doctor.OK, Msg: "running as root"},
		{ID: "nft", Severity: doctor.Blocker, Msg: "nft is not installed", Fix: "sudo apt install nftables"},
		{ID: "dns", Severity: doctor.Warn, Msg: "DNS answers look tampered with"},
	}
	m := toSetting(t, f, "doctor")
	if cli := footerCLI(m); cli != "sudo splitdpi doctor" {
		t.Errorf("CLI %q", cli)
	}
	m = press(t, m, "enter")
	if !f.called("doctor") || !m.settings.doctorOpen {
		t.Fatalf("calls %q, open %v", f.calls, m.settings.doctorOpen)
	}
	v := pageText(m.View())
	has(t, v, "What SplitDPI needs to run, problems first:")
	has(t, v, m.g.fail+" nft is not installed")
	has(t, v, "$ sudo apt install nftables")
	has(t, v, "! DNS answers look tampered with")
	has(t, v, m.g.on+" running as root")
	if strings.Index(v, "nft is not") > strings.Index(v, "running as root") {
		t.Errorf("problems not first:\n%s", v)
	}
	m = press(t, m, "r")
	if f.count("doctor") != 2 {
		t.Errorf("r did not check again: %q", f.calls)
	}
	m = press(t, press(t, m, "q"), "esc")
	if m.settings.doctorOpen || m.quitting {
		t.Fatalf("esc: open %v, quitting %v", m.settings.doctorOpen, m.quitting)
	}
	has(t, fullList(m), "Check the system           1 problem")

	f.checks = nil
	m = press(t, press(t, m, "enter"), "esc")
	has(t, fullList(m), "no problems")
}

func TestSettingsTakeoverAndRevert(t *testing.T) {
	f := newFake()
	f.foreign = []ops.Foreign{zapret2()}
	f.takeoverRep = takeoverReport()
	f.revertRep = ops.RevertReport{Removed: []string{"nft table inet splitdpi"}, Restored: []string{"zapret2 (systemd)"}}
	m := goTo(t, newModel(t, f), pgSettings)
	if got := shownIDs(m); !slices.Contains(got, "takeover") || slices.Contains(got, "revert") {
		t.Fatalf("rows %q", got)
	}
	has(t, fullList(m), "Switch from zapret         zapret2 is running")
	m = toRow(t, m, "takeover")
	if cli := footerCLI(m); cli != "sudo splitdpi takeover --yes" {
		t.Errorf("CLI %q", cli)
	}
	m = press(t, m, "enter")
	if m.dialog == nil {
		t.Fatal("no confirm")
	}
	m = press(t, m, "y")
	if !f.called("takeover zapret2") {
		t.Fatalf("calls %q", f.calls)
	}
	if got := shownIDs(m); slices.Contains(got, "takeover") || !slices.Contains(got, "revert") {
		t.Fatalf("after takeover rows %q", got)
	}
	has(t, fullList(m), "Go back to zapret          zapret2 (systemd)")

	m = toRow(t, m, "revert")
	if cli := footerCLI(m); cli != "sudo splitdpi revert" {
		t.Errorf("CLI %q", cli)
	}
	m = press(t, m, "enter")
	if m.dialog == nil {
		t.Fatal("no confirm")
	}
	v := m.View()
	has(t, v, "Go back to zapret?")
	has(t, v, "zapret2 (systemd) is turned back on.")
	has(t, v, "sudo splitdpi takeover --yes")
	m = press(t, m, "n")
	if f.called("revert false false") {
		t.Fatal("n reverted")
	}
	m = press(t, press(t, m, "enter"), "y")
	if !f.called("revert false false") {
		t.Fatalf("calls %q", f.calls)
	}
	if slices.Contains(shownIDs(m), "revert") {
		t.Error("revert row still shown")
	}
	v = flat(m.View())
	has(t, v, "Turned back on: zapret2 (systemd)")
	has(t, v, i18n.T("settings_revert.done"))
}

// Revert stops the bypass this window runs before undoing the takeover.
func TestSettingsRevertStopsOwnRun(t *testing.T) {
	f := newFake()
	f.takenOver = []string{"zapret2 (systemd)"}
	m := newModel(t, f)
	m, cmd := update(t, m, key("r"))
	go cmd()
	<-f.runStart
	m = press(t, toRow(t, goTo(t, m, pgSettings), "revert"), "enter")
	m = press(t, m, "y")
	if i, j := slices.Index(f.calls, "run ended"), slices.Index(f.calls, "revert false false"); i < 0 || j < i {
		t.Errorf("calls %q", f.calls)
	}
}

// settingsFits checks that a view fills the 80x24 screen exactly and lacks no key.
func settingsFits(t *testing.T, name, v string) {
	t.Helper()
	lines := strings.Split(v, "\n")
	if len(lines) != 24 {
		t.Errorf("%s: %d lines, want 24:\n%s", name, len(lines), v)
	}
	for _, l := range lines {
		if n := ansi.StringWidth(l); n > 80 {
			t.Errorf("%s: %d columns: %q", name, n, l)
		}
	}
	if k := missingKey.FindString(v); k != "" {
		t.Errorf("%s: missing key %s", name, k)
	}
}

// shows checks that the page shows text in full, wrapped or not.
func shows(t *testing.T, name, v, text string) {
	t.Helper()
	if !strings.Contains(flat(pageText(v)), flat(ansi.Strip(text))) {
		t.Errorf("%s: %q is cut off:\n%s", name, text, v)
	}
}

func TestSettingsStatesFit80x24(t *testing.T) {
	defer i18n.Init("en")
	for _, lang := range []string{"en", "tr", "ru"} {
		i18n.Init(lang)
		f := newFake()
		f.st.State = "running"
		f.excluded = []string{"bank.example"}
		f.foreign = []ops.Foreign{zapret2()}
		f.takenOver = []string{"zapret.service (systemd)"}
		f.set.Version = "2026.10.01"
		for i := range 14 {
			f.checks = append(f.checks, doctor.Check{ID: "c", Severity: doctor.Severity(i % 3), Msg: strings.Repeat("a check message that wraps ", 3), Fix: "sudo fix --everything"})
		}
		base := goTo(t, newModel(t, f), pgSettings)
		views := map[string]string{}
		for i, st := range base.shownSettings() {
			m := base
			for range i {
				m = press(t, m, "down")
			}
			if rowID(m) != st.id {
				t.Fatalf("%s: on %s, want %s", lang, rowID(m), st.id)
			}
			name := lang + " row " + st.id
			views[name] = m.View()
			shows(t, name, views[name], i18n.T("settings_help."+st.id))
		}
		s := press(t, base, "/")
		views[lang+" search empty"] = s.View()
		views[lang+" search dns"] = typeText(t, s, "dns").View()
		views[lang+" search none"] = typeText(t, s, "zzzz").View()
		views[lang+" search kept"] = press(t, typeText(t, s, "a"), "enter").View()

		for _, id := range []string{"lang", "run_mode", "mode"} {
			p := press(t, toRow(t, base, id), "enter")
			name := lang + " picker " + id
			views[name] = p.View()
			c := p.choicesFor(id)
			for _, n := range c.notes {
				shows(t, name, views[name], n)
			}
		}
		f.cfg.Apps = nil
		r := press(t, press(t, press(t, toRow(t, base, "mode"), "enter"), "down"), "enter")
		views[lang+" mode refused"] = r.View()
		shows(t, lang+" mode refused", r.View(), i18n.T("settings_mode.empty"))
		f.cfg.Apps = []string{"discord"}

		q := press(t, toRow(t, base, "qnum"), "enter")
		views[lang+" qnum"] = q.View()
		shows(t, lang+" qnum", q.View(), i18n.T("settings_qnum.warn"))
		q = press(t, typeText(t, clearInput(t, q), "x"), "enter")
		views[lang+" qnum not number"] = q.View()
		shows(t, lang+" qnum not number", q.View(), i18n.T("settings_qnum.not_number"))
		q = press(t, typeText(t, clearInput(t, q), "99999"), "enter")
		views[lang+" qnum refused"] = q.View()
		shows(t, lang+" qnum refused", q.View(), i18n.T("settings_qnum.warn"))

		u := press(t, toRow(t, base, "ipinfo_url"), "enter")
		u = press(t, typeText(t, clearInput(t, u), "http://"+strings.Repeat("long.", 20)+"example/json"), "enter")
		views[lang+" ipinfo refused"] = u.View()
		shows(t, lang+" ipinfo refused", u.View(), i18n.T("settings_ipinfo.default", config.Default().IPInfoURL))

		e := press(t, toRow(t, base, "skip_ifaces"), "enter")
		views[lang+" ifaces"] = e.View()
		e = press(t, e, "a")
		views[lang+" ifaces adding"] = e.View()
		views[lang+" ifaces refused"] = press(t, typeText(t, e, "bad name"), "enter").View()
		f.cfg.SkipIfaces = []string{}
		views[lang+" ifaces empty"] = press(t, toSetting(t, f, "skip_ifaces"), "enter").View()
		f.cfg.SkipIfaces = config.Default().SkipIfaces

		d := press(t, toRow(t, base, "doctor"), "enter")
		views[lang+" doctor"] = d.View()
		views[lang+" doctor done"] = press(t, d, "esc").View()

		pr := toRow(t, base, "presets")
		busy, _ := update(t, pr, key("enter"))
		views[lang+" presets busy"] = busy.View()
		views[lang+" presets done"] = press(t, pr, "enter").View()
		f.presetsErr = errors.New(strings.Repeat("the preset server said no ", 4))
		views[lang+" presets failed"] = press(t, pr, "enter").View()
		f.presetsErr = nil
		f.engineErr = errors.New(strings.Repeat("download failed ", 6))
		views[lang+" engine failed"] = press(t, toRow(t, base, "engine"), "enter").View()
		f.engineErr = nil

		views[lang+" takeover confirm"] = press(t, toRow(t, base, "takeover"), "enter").View()
		rv := press(t, toRow(t, base, "revert"), "enter")
		views[lang+" revert confirm"] = rv.View()
		shows(t, lang+" revert confirm", rv.View(), i18n.T("confirm_keys.no")+" "+i18n.T("shell.confirm.no"))
		f.revertRep = ops.RevertReport{Removed: []string{"splitdpi service (systemd)", "nft table inet splitdpi", "/run/splitdpi"}, Restored: []string{"zapret.service (systemd)"}}
		views[lang+" reverted"] = press(t, rv, "y").View()
		for name, v := range views {
			settingsFits(t, name, v)
		}
	}
}
