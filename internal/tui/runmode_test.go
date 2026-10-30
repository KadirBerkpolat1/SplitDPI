package tui

import (
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/KadirBerkpolat1/SplitDPI/internal/config"
	"github.com/KadirBerkpolat1/SplitDPI/internal/i18n"
	"github.com/KadirBerkpolat1/SplitDPI/internal/supervisor"
)

// startCalls are the fake's calls that start, stop or enable the bypass.
var startCalls = []string{"background", "enable", "stop", "run ended"}

func TestSettingsRunModePicker(t *testing.T) {
	f := newFake()
	m := toSetting(t, f, "run_mode")
	has(t, m.View(), "Run mode")
	has(t, m.View(), "In this window")
	if cli := footerCLI(m); cli != "sudo splitdpi run" {
		t.Errorf("row CLI %q", cli)
	}

	m = press(t, m, "enter")
	if p := m.settings.pick; p == nil || p.id != "run_mode" || p.cursor != 0 {
		t.Fatalf("picker %+v, want open on window", p)
	}
	v := flat(pageText(m.View()))
	has(t, v, "How should r on the Status page start the bypass?")
	for _, mode := range runModes {
		has(t, v, i18n.T("run_mode."+string(mode)))
		has(t, v, i18n.T("run_mode."+string(mode)+"_note"))
	}
	for _, want := range []string{"sudo splitdpi run --background", "sudo splitdpi enable", "sudo splitdpi run"} {
		m = press(t, m, "down")
		if cli := footerCLI(m); cli != want {
			t.Errorf("picker CLI %q, want %q", cli, want)
		}
	}
	m = press(t, press(t, m, "q"), "esc")
	if m.settings.pick != nil || m.quitting || slices.ContainsFunc(f.calls, func(c string) bool { return strings.HasPrefix(c, "run mode ") }) {
		t.Fatalf("esc: picker %v, quitting %v, calls %q", m.settings.pick, m.quitting, f.calls)
	}

	m = press(t, press(t, press(t, m, "enter"), "down"), "enter")
	if m.settings.pick != nil || m.dialog != nil {
		t.Fatalf("choosing left picker %v, dialog %v", m.settings.pick, m.dialog)
	}
	if !f.called("run mode background") {
		t.Fatalf("not saved; calls %q", f.calls)
	}
	for _, c := range startCalls {
		if f.called(c) {
			t.Errorf("choosing started something: calls %q", f.calls)
		}
	}
	if m.running || m.bgStarting || f.runCtx != nil {
		t.Error("choosing started a run")
	}
	v = flat(m.View())
	has(t, v, "Run mode: In the background. It applies the next time you press r on the Status page.")
	has(t, v, "Run mode In the background")
	if cli := footerCLI(m); cli != "sudo splitdpi run --background" {
		t.Errorf("row CLI after choosing %q", cli)
	}
	if m = press(t, m, "enter"); m.settings.pick.cursor != 1 {
		t.Errorf("picker reopened on %d, want the saved background", m.settings.pick.cursor)
	}
}

// statusIn opens the status page with mode saved and the footer focus on r.
func statusIn(t *testing.T, f *fake, mode config.RunMode) Model {
	t.Helper()
	f.cfg.RunMode = mode
	m := press(t, press(t, newModel(t, f), "tab"), "right") // into the page, past w
	if k := (statusPage{}).Actions(m)[m.focus].Key; k != "r" {
		t.Fatalf("focus on %q, want r", k)
	}
	return m
}

func TestStatusRunFollowsMode(t *testing.T) {
	for _, tc := range []struct {
		mode  config.RunMode
		label string
		cli   string
		b     bool
	}{
		{config.RunWindow, "r Run ", "sudo splitdpi run", true},
		{config.RunBackground, "r Run (in background)", "sudo splitdpi run --background", false},
		{config.RunBoot, "r Run (also at boot)", "sudo splitdpi enable", true},
	} {
		t.Run(string(tc.mode), func(t *testing.T) {
			m := statusIn(t, newFake(), tc.mode)
			v := m.View()
			has(t, v, tc.label)
			if cli := footerCLI(m); cli != tc.cli {
				t.Errorf("CLI %q, want %q", cli, tc.cli)
			}
			if got := strings.Contains(v, "b Run in background"); got != tc.b {
				t.Errorf("b offered %v, want %v:\n%s", got, tc.b, v)
			}
			hintKey := "b"
			if !tc.b {
				hintKey = "r"
			}
			has(t, flat(v), hintKey+" Keeps running when the terminal closes")
		})
	}
}

func TestStatusRunWindow(t *testing.T) {
	f := newFake()
	f.cfg.RunMode = config.RunWindow
	m, done := runHere(t, f)
	if !m.running || f.called("background") || f.called("enable") || m.dialog != nil {
		t.Fatalf("r did not run here: running %v, dialog %v, calls %q", m.running, m.dialog, f.calls)
	}
	m.stopRun()
	<-done
}

func TestStatusRunBackground(t *testing.T) {
	f := newFake()
	m := press(t, statusIn(t, f, config.RunBackground), "r")
	if !f.called("background") || m.running || m.dialog != nil || f.runCtx != nil {
		t.Fatalf("r did not start the background run: running %v, dialog %v, calls %q", m.running, m.dialog, f.calls)
	}
	has(t, m.View(), "Running in the background (pid 4343)")
	if m = press(t, m, "s"); !f.called("stop") {
		t.Error("s did not stop the background run")
	}
}

func TestStatusRunBoot(t *testing.T) {
	f := newFake()
	m := press(t, statusIn(t, f, config.RunBoot), "r")
	if m.dialog == nil || f.called("enable") {
		t.Fatalf("r did not ask first: dialog %v, calls %q", m.dialog, f.calls)
	}
	has(t, m.View(), "Start SplitDPI at boot?")
	has(t, m.View(), "sudo splitdpi disable")
	if m = press(t, m, "n"); m.dialog != nil || f.called("enable") {
		t.Fatalf("n: dialog %v, calls %q", m.dialog, f.calls)
	}
	m = press(t, press(t, m, "r"), "y")
	if f.count("enable") != 1 || m.running || f.called("background") {
		t.Fatalf("calls %q, running %v", f.calls, m.running)
	}
	if m.page != pgStatus || !m.inPage {
		t.Errorf("enabling moved away: page %d, in page %v", m.page, m.inPage)
	}
	has(t, m.View(), i18n.T("service.enabled"))

	// A running service is stopped by s, as before; nothing disables it.
	f.st = supervisor.Status{State: "running", Mode: "service", PID: 77}
	m = newModel(t, f)
	if strings.Contains(m.View(), "r Run") {
		t.Error("r offered while the service runs")
	}
	if m = press(t, m, "s"); !f.called("stop") {
		t.Errorf("s did not stop the service; calls %q", f.calls)
	}
}

// The status page fits 80x24 in every mode, with r next to its label and
// its mode's command shown.
func TestStatusRunModeFit80x24(t *testing.T) {
	defer i18n.Init("en")
	for _, lang := range []string{"en", "tr", "ru"} {
		i18n.Init(lang)
		for _, mode := range runModes {
			m := statusIn(t, newFake(), mode)
			v := m.View()
			lines := strings.Split(v, "\n")
			if len(lines) != 24 {
				t.Errorf("%s %s: %d lines, want 24:\n%s", lang, mode, len(lines), v)
			}
			for _, l := range lines {
				if n := lipgloss.Width(l); n > 80 {
					t.Errorf("%s %s: %d columns: %q", lang, mode, n, l)
				}
			}
			if k := missingKey.FindString(v); k != "" {
				t.Errorf("%s %s: missing key %s", lang, mode, k)
			}
			if label := (statusPage{}).Actions(m)[m.focus].Label; !strings.Contains(v, "r "+label) {
				t.Errorf("%s %s: r is not next to %q:\n%s", lang, mode, label, v)
			}
			if cli := footerCLI(m); cli != runModeCLI(mode) {
				t.Errorf("%s %s: CLI %q", lang, mode, cli)
			}
		}
	}
}
