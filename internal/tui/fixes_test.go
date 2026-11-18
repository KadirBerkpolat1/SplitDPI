package tui

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/KadirBerkpolat1/SplitDPI/internal/config"
	"github.com/KadirBerkpolat1/SplitDPI/internal/doctor"
	"github.com/KadirBerkpolat1/SplitDPI/internal/i18n"
	"github.com/KadirBerkpolat1/SplitDPI/internal/initsys"
	"github.com/KadirBerkpolat1/SplitDPI/internal/ops"
	"github.com/KadirBerkpolat1/SplitDPI/internal/presets"
)

// appsModeFake keeps the enabled apps as mode apps does: disabling the last
// one fails.
type appsModeFake struct {
	*fake
	on []string
}

func (a *appsModeFake) AppsEnable(id string) error {
	a.on = append(a.on, id)
	return nil
}

func (a *appsModeFake) AppsDisable(id string) error {
	if len(a.on) == 1 && a.on[0] == id {
		return ops.ErrAppsModeEmpty
	}
	a.on = slices.DeleteFunc(a.on, func(s string) bool { return s == id })
	return nil
}

// A-7: swapping the only app for one later in the alphabet works in mode apps.
func TestSaveAppsEnablesFirst(t *testing.T) {
	f := newFake()
	f.set = trSet()
	b := &appsModeFake{fake: f, on: []string{"discord"}}
	m := newModel(t, f)
	m.b = b
	m.cfg.Apps = []string{"discord"}
	m.wiz.apps = []presets.App{f.set.Apps["discord"], f.set.Apps["youtube"]}
	m.wiz.chosen = map[string]bool{"youtube": true}
	if msg := m.saveApps()().(appsAppliedMsg); msg.err != nil {
		t.Errorf("saveApps: %v", msg.err)
	}
	if !slices.Equal(b.on, []string{"youtube"}) {
		t.Errorf("apps on %q, want [youtube]", b.on)
	}
}

// A-9: run mode boot on an init without a service starts in this window.
func TestBootModeWithoutInitServiceStartsHere(t *testing.T) {
	f := newFake()
	f.svc = ops.ServiceState{}
	f.cfg.RunMode = config.RunBoot
	f.cfg.Strategy = &config.Strategy{ID: "tt-multidisorder"}
	m := newModel(t, f)
	if m.runMode() != config.RunWindow {
		t.Errorf("run mode %q, want window", m.runMode())
	}
	m, cmd := update(t, m, key("r"))
	if !m.running || cmd == nil {
		t.Errorf("r did not start the bypass: note %q", m.note)
	}
}

// A-11: the system check scrolls at 80x24, with what is cut off counted.
func TestDoctorViewScrolls(t *testing.T) {
	m := newModel(t, newFake())
	m.page = pgSettings
	m.settings.doctorOpen = true
	for i := range 16 {
		c := doctor.Check{Severity: doctor.OK, Msg: fmt.Sprintf("check item %c", 'A'+i)}
		if i < 3 {
			c.Severity, c.Fix = doctor.Warn, "sudo splitdpi deps install"
		}
		m.settings.checks = append(m.settings.checks, c)
	}
	last := "check item P"
	if v := m.View(); strings.Contains(v, last) || !strings.Contains(v, unicodeGlyphs.down+" ") {
		t.Fatalf("the first view shows the last check or no ↓ count:\n%s", v)
	}
	for range 30 {
		if strings.Contains(m.View(), last) {
			break
		}
		m = press(t, m, "down")
	}
	v := m.View()
	if !strings.Contains(v, last) || !strings.Contains(v, unicodeGlyphs.up+" ") {
		t.Fatalf("↓ does not scroll to the last check with an ↑ count:\n%s", v)
	}
	if lines := strings.Split(v, "\n"); len(lines) != 24 {
		t.Errorf("view has %d lines, want 24", len(lines))
	}
	for range 30 {
		m = press(t, m, "k")
	}
	if v := m.View(); !strings.Contains(v, "check item A") || strings.Contains(v, unicodeGlyphs.up+" ") {
		t.Errorf("k does not scroll back to the top:\n%s", m.View())
	}
}

// A-12: a takeover that imports sites into a running SplitDPI asks for R and
// does not say to start it.
func TestTakeoverWhileRunningAsksRestart(t *testing.T) {
	f := newFake()
	runningService(f)
	z := ops.Foreign{Kind: "zapret", Service: initsys.ForeignService{Name: "zapret", Enabled: true}, Hostlist: "/opt/zapret/ipset/zapret-hosts-user.txt"}
	f.foreign = []ops.Foreign{z}
	f.takeoverRep = ops.TakeoverReport{Added: []string{"rutracker.org"}, Disabled: true}
	m := newModel(t, f)
	m, _ = update(t, m, m.takeover([]ops.Foreign{z})())
	if !m.pending {
		t.Error("the imported sites wait for a restart, but none is pending")
	}
	if m.note != i18n.T("fix_arayuz.takeover_done_running")+" "+i18n.T("tui.restart_hint", restartKey) {
		t.Errorf("note %q", m.note)
	}
}

// A-13: a cancelled scan whose service did not start again does not say
// nothing was changed.
func TestCancelledScanResumeFailure(t *testing.T) {
	f := newFake()
	runningService(f)
	started := blockingScan(f)
	f.enableErr = errors.New("the service did not start")
	m, cmd := toScan(t, f)
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	<-started
	m = press(t, m, "esc")
	m, _ = update(t, m, <-done)
	v := flat(ansi.Strip(setupPage{}.View(m, 200, 20)))
	if strings.Contains(v, i18n.T("tui.wiz.cancelled")) {
		t.Errorf("says nothing was changed though the service is down:\n%s", v)
	}
	has(t, v, i18n.T("fix_arayuz.cancelled_not_restarted"))
}
