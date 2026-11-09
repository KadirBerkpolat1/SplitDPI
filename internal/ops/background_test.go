package ops

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"testing/fstest"
	"time"

	"github.com/KadirBerkpolat1/SplitDPI/internal/config"
	"github.com/KadirBerkpolat1/SplitDPI/internal/paths"
)

// No test here runs systemd-run or systemctl, or forks splitdpi: fakeUnits
// answers for systemd and Detach is faked.

// runPID is the PID a faked background run records. It cannot exist: a wrong
// code path must fail, not signal anything.
const runPID = 999999999

func withSystemd() fstest.MapFS { return fstest.MapFS{"run/systemd/system": {Mode: os.ModeDir}} }

// fakeUnits answers systemctl and systemd-run for backgroundUnit and records
// their command lines; any other command succeeds with no output.
type fakeUnits struct {
	mu      sync.Mutex
	state   string // ActiveState of the unit
	after   string // its ActiveState once systemd-run has started it
	runErr  error  // what systemd-run fails with
	started func() // the run of the unit coming up
	calls   [][]string
}

func (f *fakeUnits) run(_ context.Context, _ string, name string, args ...string) ([]byte, error) {
	if name != "systemctl" && name != "systemd-run" {
		return nil, nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, append([]string{name}, args...))
	switch {
	case name == "systemd-run" && f.runErr != nil:
		return nil, f.runErr
	case name == "systemd-run":
		f.state = f.after
		if f.started != nil {
			f.started()
		}
	case args[0] == "reset-failed":
		f.state = "inactive"
	case args[0] == "show":
		return []byte(f.state + "\n"), nil
	}
	return nil, nil
}

func (f *fakeUnits) commands() [][]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

// throughRun cuts calls after systemd-run: what follows are polls of the unit.
func throughRun(calls [][]string) [][]string {
	return calls[:slices.IndexFunc(calls, func(c []string) bool { return c[0] == "systemd-run" })+1]
}

// backgroundTestOps is ready for Background on root. f answers for systemd, a
// unit started by systemd-run comes up running, and Detach fails the test.
func backgroundTestOps(t *testing.T, root fstest.MapFS) (*Ops, *fakeUnits) {
	t.Helper()
	o, _, _ := trialOps(t, root)
	cfg := trialConfig()
	cfg.Strategy = &config.Strategy{ID: "tt", TCPTLS: candidate.TCPTLS}
	if err := config.Save(o.P, cfg); err != nil {
		t.Fatal(err)
	}
	f := &fakeUnits{state: "inactive", after: "active", started: func() { holdBackgroundLock(t, o.P) }}
	o.Exec = f.run
	o.Detach = func(*exec.Cmd) (<-chan error, error) {
		t.Error("detached process started")
		return nil, errors.New("unexpected")
	}
	return o, f
}

// holdBackgroundLock makes p look like a background run under runPID is up.
func holdBackgroundLock(t *testing.T, p paths.Paths) {
	t.Helper()
	if err := os.MkdirAll(p.RunDir, 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(p.LockFile, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	if _, err := fmt.Fprintf(f, `{"pid":%d,"mode":"background"}`, runPID); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	st := fmt.Sprintf(`{"state":"running","mode":"background","pid":%d}`, runPID)
	if err := os.WriteFile(p.StatusFile, []byte(st), 0o644); err != nil {
		t.Fatal(err)
	}
}

func setEnviron(t *testing.T, env ...string) {
	old := environ
	environ = func() []string { return env }
	t.Cleanup(func() { environ = old })
}

// On systemd a background run is a transient system unit that nothing enables
// or restarts. It logs to LogFile, gets the variables the run reads, and a stop
// sends SIGTERM to each of its processes, with SIGKILL only 15 s later.
func TestBackgroundStartsTransientUnit(t *testing.T) {
	o, f := backgroundTestOps(t, withSystemd())
	setEnviron(t, "PATH=/usr/bin", "HOME=/root", "SPLITDPI_ROOT=/srv/splitdpi", "LANG=tr_TR.UTF-8",
		"LANGUAGE=de", "LC_MESSAGES=ru_RU.UTF-8", "SUDO_USER=berk")
	pid, err := o.Background(context.Background())
	if err != nil || pid != runPID {
		t.Fatalf("got pid %d, %v", pid, err)
	}
	self, _ := os.Executable()
	want := [][]string{
		{"systemctl", "show", "--property=ActiveState", "--value", "splitdpi-background.service"},
		{
			"systemd-run",
			"--unit=splitdpi-background.service",
			"--description=SplitDPI background run",
			"--service-type=exec",
			"--collect",
			"--property=Restart=no",
			"--property=KillMode=control-group",
			"--property=KillSignal=SIGTERM",
			"--property=TimeoutStopSec=15s",
			"--property=StandardOutput=append:" + o.P.LogFile,
			"--property=StandardError=append:" + o.P.LogFile,
			"--setenv=SPLITDPI_ROOT=/srv/splitdpi",
			"--setenv=LANG=tr_TR.UTF-8",
			"--setenv=LC_MESSAGES=ru_RU.UTF-8",
			"--", self, "run", DetachedArg,
		},
	}
	if got := throughRun(f.commands()); !slices.EqualFunc(got, want, slices.Equal[[]string]) {
		t.Errorf("commands\n%q\nwant\n%q", got, want)
	}
	if b, _ := os.ReadFile(o.P.LogFile); len(b) != 0 {
		t.Errorf("log %q, want nothing from the starting process", b)
	}
}

// systemd-run refuses the name while a failed unit holds it, so that one is reset first.
func TestBackgroundResetsFailedUnit(t *testing.T) {
	o, f := backgroundTestOps(t, withSystemd())
	f.state = "failed"
	if _, err := o.Background(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := throughRun(f.commands())
	if len(got) != 3 || got[0][1] != "show" ||
		!slices.Equal(got[1], []string{"systemctl", "reset-failed", "splitdpi-background.service"}) || got[2][0] != "systemd-run" {
		t.Errorf("commands %q, want show, reset-failed, then systemd-run", got)
	}
}

// Without systemd or systemd-run, or when systemd-run fails, the run starts as
// before: a child in a session of its own that writes to LogFile.
func TestBackgroundFallsBackToDetached(t *testing.T) {
	const runErr = "systemd-run: exit status 1: Failed to start transient service unit: Unit splitdpi-background.service was already loaded or has a fragment file."
	for _, tc := range []struct {
		name  string
		root  fstest.MapFS
		setup func(*Ops, *fakeUnits)
		tried bool // systemd-run was run, and its error is in the log
	}{
		{name: "not systemd", root: fstest.MapFS{}},
		{name: "no systemd-run", root: withSystemd(), setup: func(o *Ops, _ *fakeUnits) {
			o.LookPath = func(name string) (string, error) {
				if name == "systemd-run" {
					return "", exec.ErrNotFound
				}
				return "/usr/bin/" + name, nil
			}
		}},
		{name: "systemd-run fails", root: withSystemd(), tried: true, setup: func(_ *Ops, f *fakeUnits) {
			f.runErr = errors.New(runErr)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o, f := backgroundTestOps(t, tc.root)
			if tc.setup != nil {
				tc.setup(o, f)
			}
			var c *exec.Cmd
			o.Detach = func(cmd *exec.Cmd) (<-chan error, error) {
				c = cmd
				holdBackgroundLock(t, o.P)
				return make(chan error), nil
			}
			pid, err := o.Background(context.Background())
			if err != nil || pid != runPID {
				t.Fatalf("got pid %d, %v", pid, err)
			}
			self, _ := os.Executable()
			if c == nil || c.Path != self || !slices.Equal(c.Args[1:], []string{"run", DetachedArg}) {
				t.Fatalf("detached command %v, want %s run %s", c, self, DetachedArg)
			}
			if c.SysProcAttr == nil || !c.SysProcAttr.Setsid {
				t.Error("not in a session of its own")
			}
			if lf, ok := c.Stderr.(*os.File); !ok || lf.Name() != o.P.LogFile || c.Stdout != c.Stderr {
				t.Errorf("output %v %v, want %s", c.Stdout, c.Stderr, o.P.LogFile)
			}
			calls := f.commands()
			b, _ := os.ReadFile(o.P.LogFile)
			if !tc.tried {
				if len(calls) != 0 || len(b) != 0 {
					t.Errorf("commands %q, log %q, want neither", calls, b)
				}
				return
			}
			if len(calls) == 0 || calls[len(calls)-1][0] != "systemd-run" {
				t.Errorf("commands %q, want systemd-run last", calls)
			}
			if string(b) != runErr+"\n" {
				t.Errorf("log %q, want the systemd-run error", b)
			}
		})
	}
}

// A unit whose run fails is reported once it has ended, not after the 10 s wait.
func TestBackgroundUnitEndsEarly(t *testing.T) {
	o, f := backgroundTestOps(t, withSystemd())
	f.after, f.started = "failed", nil
	start := time.Now()
	_, err := o.Background(context.Background())
	if !errors.As(err, &BackgroundNotStartedError{}) || !strings.Contains(err.Error(), o.P.LogFile) {
		t.Fatalf("got %v, want BackgroundNotStartedError naming the log", err)
	}
	if d := time.Since(start); d >= enableWait/2 {
		t.Errorf("reported after %s", d)
	}
}
