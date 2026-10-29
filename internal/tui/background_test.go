package tui

import (
	"bytes"
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/KadirBerkpolat1/SplitDPI/internal/i18n"
	"github.com/KadirBerkpolat1/SplitDPI/internal/ops"
)

func TestBackgroundAction(t *testing.T) {
	f := newFake()
	m := newModel(t, f)
	v := m.View()
	has(t, v, "b Run in background")
	has(t, flat(v), "Keeps running when the terminal closes")
	for _, k := range []string{"tab", "right", "right"} { // into the page, past w and r
		m = press(t, m, k)
	}
	has(t, m.View(), "$ sudo splitdpi run --background")

	m = press(t, m, "b")
	if !f.called("background") {
		t.Fatal("b did not start the background run")
	}
	has(t, m.View(), "Running in the background (pid 4343)")
	body := flat(statusPage{}.View(m, 60, 18))
	has(t, body, "background (stops at reboot), pid 4343")
	if strings.Contains(m.View(), "b Run in background") {
		t.Error("b still offered while the background run is on")
	}
	m = press(t, m, "s")
	if !f.called("stop") {
		t.Error("s did not stop the background run")
	}
}

func TestBackgroundActionFailure(t *testing.T) {
	f := newFake()
	f.bgErr = ops.BackgroundNotStartedError{Log: "/var/log/splitdpi.log"}
	m := press(t, newModel(t, f), "b")
	has(t, flat(m.View()), "did not start in the background")
	has(t, flat(m.View()), "/var/log/splitdpi.log")
	has(t, m.View(), "b Run in background")
}

// runHere starts "Run now" and returns once the fake runs it; done delivers
// when the run has returned.
func runHere(t *testing.T, f *fake) (Model, chan struct{}) {
	t.Helper()
	m, cmd := update(t, newModel(t, f), key("r"))
	done := make(chan struct{})
	go func() { cmd(); close(done) }()
	<-f.runStart
	return m, done
}

// The quit prompt of a bypass run in this window offers three ways out, with
// the confirm dialog's keys in each language plus b.
func TestQuitPromptThreeWay(t *testing.T) {
	defer i18n.Init("en")
	for _, lang := range []string{"en", "tr"} {
		i18n.Init(lang)
		yes, no := i18n.T("confirm_keys.yes"), i18n.T("confirm_keys.no")

		t.Run(lang+" stop", func(t *testing.T) {
			f := newFake()
			m, done := runHere(t, f)
			m, _ = update(t, m, key("q"))
			v := m.View()
			has(t, v, i18n.T("background_tui.quit"))
			for k, label := range map[string]string{yes: "quit_stop", "b": "quit_background", no: "quit_cancel"} {
				has(t, v, k+" "+i18n.T("background_tui."+label))
			}
			m, _ = update(t, m, key(yes))
			if f.runCtx.Err() == nil {
				t.Fatal("run not cancelled")
			}
			<-done
			m, cmd := update(t, m, runDoneMsg{})
			if _, ok := cmd().(tea.QuitMsg); !ok || f.called("background") {
				t.Errorf("did not just quit; calls %q", f.calls)
			}
		})

		t.Run(lang+" background", func(t *testing.T) {
			f := newFake()
			m, done := runHere(t, f)
			m, _ = update(t, m, key("q"))
			m, cmd := update(t, m, key("b"))
			if cmd != nil || f.called("background") {
				t.Fatal("background started before the run here stopped")
			}
			<-done
			m, cmd = update(t, m, runDoneMsg{})
			m, cmd = update(t, m, cmd())
			if _, ok := cmd().(tea.QuitMsg); !ok {
				t.Fatal("did not quit once the background run was up")
			}
			if i, j := slices.Index(f.calls, "run ended"), slices.Index(f.calls, "background"); i < 0 || j < i {
				t.Errorf("calls %q: want the run here ended, then the background run", f.calls)
			}
			var out bytes.Buffer
			m.printBackground(&out)
			has(t, out.String(), i18n.T("background.started", 4343))
		})

		t.Run(lang+" background fails", func(t *testing.T) {
			f := newFake()
			f.bgErr = ops.BackgroundNotStartedError{Log: "/var/log/splitdpi.log"}
			m, done := runHere(t, f)
			m, _ = update(t, m, key("q"))
			m, _ = update(t, m, key("b"))
			<-done
			m, cmd := update(t, m, runDoneMsg{})
			m, cmd = update(t, m, cmd())
			if _, ok := cmd().(tea.QuitMsg); ok || m.quitting {
				t.Fatal("quit although the background run did not start")
			}
			has(t, flat(m.View()), "/var/log/splitdpi.log")
			var out bytes.Buffer
			m.printBackground(&out)
			if out.Len() != 0 {
				t.Errorf("printed %q without a background run", out.String())
			}
		})

		t.Run(lang+" cancel", func(t *testing.T) {
			f := newFake()
			m, done := runHere(t, f)
			m, _ = update(t, m, key("q"))
			m, _ = update(t, m, key(no))
			if m.confirmQuit || m.quitting || f.runCtx.Err() != nil {
				t.Errorf("%s did not cancel: prompt %v quitting %v", no, m.confirmQuit, m.quitting)
			}
			m.stopRun()
			<-done
		})
	}
}
