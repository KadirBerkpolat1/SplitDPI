package tui

import (
	"bytes"
	"errors"
	"slices"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/KadirBerkpolat1/SplitDPI/internal/config"
	"github.com/KadirBerkpolat1/SplitDPI/internal/i18n"
	"github.com/KadirBerkpolat1/SplitDPI/internal/ops"
	"github.com/KadirBerkpolat1/SplitDPI/internal/supervisor"
)

// inBackgroundMode has a method to run with and run mode background: r
// starts the bypass in the background.
func inBackgroundMode(f *fake) *fake {
	f.cfg.Strategy = &config.Strategy{ID: "tt-multidisorder"}
	f.cfg.RunMode = config.RunBackground
	return f
}

func TestBackgroundAction(t *testing.T) {
	f := inBackgroundMode(newFake())
	m := newModel(t, f)
	has(t, m.View(), "r Start")
	has(t, flat(m.View()), i18n.T("tui.how.start_background"))
	if cli := cliFor(m, "r"); cli != "sudo splitdpi run --background" {
		t.Errorf("start CLI %q", cli)
	}

	m = press(t, m, "r")
	if !f.called("background") {
		t.Fatal("r did not start the background run")
	}
	has(t, m.View(), "Running in the background (pid 4343)")
	has(t, flat(statusPage{}.View(m, 60, 18)), i18n.T("tui.how.background"))
	if cliFor(m, "r") != "" {
		t.Error("r still offered while the background run is on")
	}
	f.st = supervisor.Status{State: "stopped"} // what Status says once Stop returns
	m = press(t, m, "s")
	if !f.called("stop") {
		t.Error("s did not stop the background run")
	}
	if m.st.State != "stopped" || cliFor(m, "r") == "" {
		t.Errorf("after s: state %q, actions %q", m.st.State, reproKeys(m))
	}
}

func TestBackgroundActionFailure(t *testing.T) {
	f := inBackgroundMode(newFake())
	f.bgErr = ops.BackgroundNotStartedError{Log: "/var/log/splitdpi.log"}
	m := press(t, newModel(t, f), "r")
	has(t, flat(m.View()), "did not start in the background")
	has(t, flat(m.View()), "/var/log/splitdpi.log")
	has(t, m.View(), "r Start")
}

// A raw error of several lines, as nft prints with its rule and a caret
// line, keeps its cause in the note at 80x24.
func TestBackgroundRawErrorKeepsCause(t *testing.T) {
	f := inBackgroundMode(newFake())
	f.bgErr = errors.New("load nft table splitdpi: nft: exit status 1: Error: Could not process rule: No such file or directory\n" +
		"add rule inet splitdpi output meta mark 0x40000000 queue num 200 bypass\n" +
		"                                                   ^^^^^^^^^^^^^^^^^^^^^")
	v := press(t, newModel(t, f), "r").View()
	fits(t, "raw error", v)
	has(t, flat(v), "Could not process rule")
}

// A log stream opened before the background run read the journal; starting
// one replaces it, and the end of the old stream leaves the new one alone.
func TestBackgroundRestartsLogStream(t *testing.T) {
	f := inBackgroundMode(newFake())
	f.logsFollow = true
	m, cmd := update(t, newModel(t, f), key("4"))
	go cmd()          // the first stream, until cancelled
	m.page = pgStatus // as if the stream had been left running
	m = press(t, m, "r")
	if m.logCancel != nil {
		t.Fatal("the stream from before the background run still runs")
	}
	m, cmd = update(t, m, key("4"))
	if cmd == nil || m.logCancel == nil {
		t.Fatal("opening the logs again started no stream")
	}
	m, _ = update(t, m, logEndMsg{stream: 1})
	if m.logCancel == nil {
		t.Error("the old stream's end stopped the new one")
	}
	m.stopAll()
}

// The log stream runs only while Logs is shown.
func TestLogsStopWhenLeaving(t *testing.T) {
	f := newFake()
	f.logsFollow = true
	m, cmd := update(t, newModel(t, f), key("4"))
	done := make(chan struct{})
	go func() { cmd(); close(done) }()
	if m.logCancel == nil {
		t.Fatal("Logs started no stream")
	}
	if m = press(t, m, "tab"); m.logCancel != nil {
		t.Error("leaving Logs kept the stream")
	}
	<-done
}

// runHere starts the bypass in this window and returns once the fake runs
// it; done delivers when the run has returned.
func runHere(t *testing.T, f *fake) (Model, chan struct{}) {
	t.Helper()
	if f.cfg.Strategy == nil {
		f.cfg.Strategy = &config.Strategy{ID: "tt-multidisorder"}
	}
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
			has(t, flat(popupText(m)), flat(i18n.T("background_tui.quit")))
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
			if m.running || !m.quitting {
				t.Errorf("after the run here ended: running %v, quitting %v", m.running, m.quitting)
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

// The run here may end by itself, in a crash loop say, while the quit
// prompt is open: its answer then has no run to stop, and y still just
// quits, b still starts the background run first.
func TestQuitPromptAfterRunEnded(t *testing.T) {
	for _, k := range []string{"y", "b"} {
		t.Run(k, func(t *testing.T) {
			f := newFake()
			m, done := runHere(t, f)
			m, _ = update(t, m, key("q"))
			end := m.runCancel
			end()
			<-done
			m, _ = update(t, m, runDoneMsg{supervisor.ErrCrashLoop})
			m, cmd := update(t, m, key(k))
			if k == "b" {
				if cmd == nil {
					t.Fatal("b started nothing")
				}
				m, cmd = update(t, m, cmd())
				if !f.called("background") || m.bgPID != 4343 {
					t.Errorf("calls %q, background pid %d", f.calls, m.bgPID)
				}
			}
			if cmd == nil {
				t.Fatal("did not quit")
			}
			if _, ok := cmd().(tea.QuitMsg); !ok {
				t.Fatal("did not quit")
			}
			if k == "y" && f.called("background") {
				t.Errorf("calls %q: y started the background run", f.calls)
			}
		})
	}
}
