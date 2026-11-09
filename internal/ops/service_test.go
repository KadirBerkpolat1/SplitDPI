package ops

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"testing/fstest"
	"time"

	"github.com/KadirBerkpolat1/SplitDPI/internal/config"
	"github.com/KadirBerkpolat1/SplitDPI/internal/paths"
	"github.com/KadirBerkpolat1/SplitDPI/internal/render"
	"github.com/KadirBerkpolat1/SplitDPI/internal/supervisor"
	"github.com/KadirBerkpolat1/SplitDPI/internal/sysinfo"
)

// Every command here is faked, and TerminateRun and holdSignals are
// replaced: no real service, table, process or signal is touched.

// lockRun makes this process look like a supervisor with pid in mode, in
// state, until release, like that supervisor exiting.
func lockRun(p paths.Paths, mode string, pid int, state string) (release func(), err error) {
	if err := os.MkdirAll(p.RunDir, 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(p.LockFile, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	// The status goes first: the mode in LockFile must not meet the status
	// of the run before.
	st := fmt.Sprintf(`{"state":%q,"mode":%q,"pid":%d}`, state, mode, pid)
	if err := os.WriteFile(p.StatusFile, []byte(st), 0o644); err != nil {
		f.Close()
		return nil, err
	}
	if err := f.Truncate(0); err != nil {
		f.Close()
		return nil, err
	}
	fmt.Fprintf(f, `{"pid":%d,"mode":%q}`, pid, mode)
	var once sync.Once
	return func() { once.Do(func() { f.Close() }) }, nil
}

// holdRunLock is lockRun of a running supervisor for the test; the lock goes
// at its end at the latest.
func holdRunLock(t *testing.T, p paths.Paths, mode string, pid int) func() {
	t.Helper()
	release, err := lockRun(p, mode, pid, "running")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(release)
	return release
}

// serviceRecorder keeps the commands run, the PIDs signalled and the signal
// holds, in one order. fail makes a command fail, systemd-run by its name
// alone, and hooks run once one is recorded. With down, a started service
// never runs: on systemd it does not come up, on runit it holds the lock
// without running; fakeRunsvdir reads down under mu.
type serviceRecorder struct {
	mu    sync.Mutex
	calls []string
	fail  map[string]error
	hooks map[string]func()
	down  bool
}

func (r *serviceRecorder) add(s string) {
	r.mu.Lock()
	r.calls = append(r.calls, s)
	r.mu.Unlock()
}

// service is the calls that change the service, a run or the signals, in
// order; systemd-run, which starts a background run, without its options.
func (r *serviceRecorder) service() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, c := range r.calls {
		switch {
		case strings.HasPrefix(c, "lsattr "), strings.HasPrefix(c, "systemctl show "):
		case strings.HasPrefix(c, "systemd-run "):
			out = append(out, "systemd-run")
		default:
			out = append(out, c)
		}
	}
	return out
}

var initHosts = map[sysinfo.Init]fstest.MapFS{
	sysinfo.Systemd: {"run/systemd/system": {Mode: fs.ModeDir}},
	sysinfo.OpenRC:  {"run/openrc": {Mode: fs.ModeDir}},
	sysinfo.Runit:   {"etc/runit": {Mode: fs.ModeDir}, "var/service": {Mode: fs.ModeDir}},
}

// backgroundPID is the supervisor of the background run that systemd-run starts.
const backgroundPID = 5151

// serviceOps can enable the service of init in with host as the host: it has
// a strategy and an engine, and starting the service makes it hold the lock
// and run. A background run started through systemd-run comes up too; one
// started detached fails the test.
func serviceOps(t *testing.T, in sysinfo.Init, host fstest.MapFS) (*Ops, *serviceRecorder) {
	t.Helper()
	for name, f := range initHosts[in] {
		host[name] = f
	}
	p := paths.Under(t.TempDir())
	c := config.Default()
	c.Strategy = &config.Strategy{ID: "tt", TCPTLS: "--payload=tls_client_hello --lua-desync=multisplit:pos=2"}
	if err := config.Save(p, c); err != nil {
		t.Fatal(err)
	}
	installedEngine(t, p, "zapret2-v1.0.7")
	rec := &serviceRecorder{}
	o := &Ops{
		P: p, Root: host, Euid: func() int { return 0 }, GOARCH: "amd64",
		LookPath: func(name string) (string, error) { return "/usr/bin/" + name, nil },
		Exec: func(_ context.Context, _ string, name string, args ...string) ([]byte, error) {
			cmd := strings.Join(append([]string{name}, args...), " ")
			rec.add(cmd)
			if h := rec.hooks[cmd]; h != nil {
				h()
			}
			if err := rec.fail[cmd]; err != nil {
				return nil, err
			}
			switch {
			case cmd == "systemctl start splitdpi" || cmd == "rc-service splitdpi start":
				if !rec.down {
					holdRunLock(t, p, "service", 4242)
				}
			case name == "systemd-run":
				if err := rec.fail[name]; err != nil {
					return nil, err
				}
				holdRunLock(t, p, "background", backgroundPID)
			}
			return nil, nil
		},
		Detach: func(*exec.Cmd) (<-chan error, error) {
			t.Error("detached process started")
			return nil, errors.New("unexpected")
		},
	}
	if in == sysinfo.Runit {
		fakeRunsvdir(t, p, rec)
	}
	old := holdSignals
	holdSignals = func() func() {
		rec.add("hold signals")
		return func() { rec.add("restore signals") }
	}
	t.Cleanup(func() { holdSignals = old })
	return o, rec
}

// fakeRunsvdir stands in for runit's runsvdir: once the service is linked,
// its run comes up and holds the lock until the test ends. With rec.down it
// never reports running, and it exits 300 ms after its link is gone: sv
// down and the unlink only ask runsv to stop it.
func fakeRunsvdir(t *testing.T, p paths.Paths, rec *serviceRecorder) {
	t.Helper()
	stop, done := make(chan struct{}), make(chan struct{})
	// linkedIs waits until the link is there, or gone; false if the test ended first.
	linkedIs := func(want bool) bool {
		tick := time.NewTicker(5 * time.Millisecond)
		defer tick.Stop()
		for {
			if _, err := os.Lstat(p.RunitLink); (err == nil) == want {
				return true
			}
			select {
			case <-stop:
				return false
			case <-tick.C:
			}
		}
	}
	go func() {
		defer close(done)
		if !linkedIs(true) {
			return
		}
		rec.mu.Lock()
		down := rec.down
		rec.mu.Unlock()
		state := "running"
		if down {
			state = "starting"
		}
		release, err := lockRun(p, "service", 4242, state)
		if err != nil {
			t.Error(err)
			return
		}
		defer release()
		if down && linkedIs(false) {
			select {
			case <-stop:
			case <-time.After(300 * time.Millisecond):
			}
			return
		}
		<-stop
	}()
	t.Cleanup(func() { close(stop); <-done })
}

// terminateWith replaces TerminateRun: it records the signal, then does what
// to the run.
func terminateWith(t *testing.T, rec *serviceRecorder, what func() error) {
	t.Helper()
	old := TerminateRun
	TerminateRun = func(pid int) error {
		rec.add(fmt.Sprintf("SIGTERM %d", pid))
		return what()
	}
	t.Cleanup(func() { TerminateRun = old })
}

// shortWait makes waiting for the service to run give up after 300 ms.
func shortWait(t *testing.T) {
	old := enableWait
	enableWait = 300 * time.Millisecond
	t.Cleanup(func() { enableWait = old })
}

// shortStopWait makes waiting for a run told to stop give up after 100 ms,
// and waiting for it once more after more.
func shortStopWait(t *testing.T, more time.Duration) {
	oldRelease, oldMore := releaseWait, takeBackWait
	releaseWait, takeBackWait = 100*time.Millisecond, more
	t.Cleanup(func() { releaseWait, takeBackWait = oldRelease, oldMore })
}

func installAt(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

// rootFile is a host file in root's hands, mode perm.
func rootFile(perm fs.FileMode) *fstest.MapFile {
	return &fstest.MapFile{Mode: perm, Sys: &syscall.Stat_t{Uid: 0}}
}

func rootDir(perm fs.FileMode) *fstest.MapFile {
	return &fstest.MapFile{Mode: fs.ModeDir | perm, Sys: &syscall.Stat_t{Uid: 0}}
}

// hostTree puts bin on host as install.sh run with sudo leaves it: the file
// and every directory above it, up to "/", root's with mode 0755.
func hostTree(host fstest.MapFS, bin string) {
	host[strings.TrimPrefix(bin, "/")] = rootFile(0o755)
	for d := filepath.Dir(bin); ; d = filepath.Dir(d) {
		host[filepath.Join(".", d)] = rootDir(0o755)
		if d == "/" {
			return
		}
	}
}

// installBinPath puts splitdpi at BinPath, root's on the host.
func installBinPath(t *testing.T, o *Ops) {
	t.Helper()
	installAt(t, o.P.BinPath)
	hostTree(o.Root.(fstest.MapFS), o.onHost(o.P.BinPath))
}

// selfAt is a host whose running splitdpi is bin, root's like the
// directories above it.
func selfAt(bin string) fstest.MapFS {
	host := fstest.MapFS{"proc/self/exe": {Mode: fs.ModeSymlink, Data: []byte(bin)}}
	hostTree(host, bin)
	return host
}

func savedRunMode(t *testing.T, p paths.Paths) config.RunMode {
	t.Helper()
	c, err := config.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	return c.RunMode
}

func takeoverError(t *testing.T, err error) TakeoverError {
	t.Helper()
	var e TakeoverError
	if !errors.As(err, &e) {
		t.Fatalf("err %v, want a TakeoverError", err)
	}
	return e
}

var enableCalls = []string{"systemctl daemon-reload", "systemctl enable splitdpi", "systemctl start splitdpi"}

// The service is installed and enabled first, with the run still up; only
// then is a background or foreground run stopped, with the signals held, and
// once it has exited the service starts in its place.
func TestEnableTakesOverRun(t *testing.T) {
	for _, mode := range []string{"background", "foreground"} {
		t.Run(mode, func(t *testing.T) {
			o, rec := serviceOps(t, sysinfo.Systemd, fstest.MapFS{})
			installBinPath(t, o)
			release := holdRunLock(t, o.P, mode, 4343)
			terminateWith(t, rec, func() error { release(); return nil })
			r, err := o.Enable(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			if r != (EnableReport{Stopped: mode, PID: 4343}) {
				t.Errorf("report %+v", r)
			}
			want := []string{"systemctl daemon-reload", "systemctl enable splitdpi", "hold signals", "SIGTERM 4343", "systemctl start splitdpi", "restore signals"}
			if !slices.Equal(rec.service(), want) {
				t.Errorf("calls %q, want %q", rec.service(), want)
			}
			if got := savedRunMode(t, o.P); got != config.RunBoot {
				t.Errorf("run mode %q, want boot", got)
			}
		})
	}
}

// The bypass this process runs (the TUI's window) is stopped through
// stopHere at the same step, and no process is signalled.
func TestEnableTakesOverRunHere(t *testing.T) {
	for name, held := range map[string]bool{"holding the lock": true, "not holding it yet": false} {
		t.Run(name, func(t *testing.T) {
			o, rec := serviceOps(t, sysinfo.Systemd, fstest.MapFS{})
			installBinPath(t, o)
			release := func() {}
			if held {
				release = holdRunLock(t, o.P, "foreground", os.Getpid())
			}
			terminateWith(t, rec, func() error { return nil })
			r, err := o.Enable(context.Background(), func() { rec.add("stop here"); release() })
			if err != nil {
				t.Fatal(err)
			}
			if r != (EnableReport{Stopped: "foreground", PID: os.Getpid()}) {
				t.Errorf("report %+v", r)
			}
			want := []string{"systemctl daemon-reload", "systemctl enable splitdpi", "hold signals", "stop here", "systemctl start splitdpi", "restore signals"}
			if !slices.Equal(rec.service(), want) {
				t.Errorf("calls %q, want %q", rec.service(), want)
			}
		})
	}
}

func TestEnableNothingToTakeOver(t *testing.T) {
	o, rec := serviceOps(t, sysinfo.Systemd, fstest.MapFS{})
	installBinPath(t, o)
	terminateWith(t, rec, func() error { return nil })
	r, err := o.Enable(context.Background(), nil)
	if err != nil || r != (EnableReport{}) {
		t.Fatalf("report %+v, err %v", r, err)
	}
	if !slices.Equal(rec.service(), enableCalls) {
		t.Errorf("calls %q", rec.service())
	}
}

// A scan, and a run of this very process that no stopHere stops, are not
// stopped: the service is left alone and so is the run mode.
func TestEnableLeavesRunItMustNotStop(t *testing.T) {
	for _, tc := range []struct {
		name, mode string
		pid        int
	}{
		{"scan", "scan", 4343},
		{"this process", "foreground", os.Getpid()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o, rec := serviceOps(t, sysinfo.Systemd, fstest.MapFS{})
			installBinPath(t, o)
			holdRunLock(t, o.P, tc.mode, tc.pid)
			terminateWith(t, rec, func() error { return nil })
			_, err := o.Enable(context.Background(), nil)
			var already supervisor.ErrAlreadyRunning
			if !errors.As(err, &already) || already.Mode != tc.mode {
				t.Errorf("err %v, want already running (%s)", err, tc.mode)
			}
			if len(rec.service()) > 0 {
				t.Errorf("calls %q", rec.service())
			}
			if got := savedRunMode(t, o.P); got != config.RunWindow {
				t.Errorf("run mode %q", got)
			}
		})
	}
}

// The bug of the first round: the run was stopped before the checks. A
// failed check now stops nothing and changes nothing.
func TestEnableCheckFailsStopsNothing(t *testing.T) {
	for _, tc := range []struct {
		name  string
		host  fstest.MapFS
		setup func(*Ops)
		want  func(error) bool
	}{
		{"unsafe binary", fstest.MapFS{}, func(o *Ops) {
			o.Root.(fstest.MapFS)["usr/local/bin/splitdpi"].Sys = &syscall.Stat_t{Uid: 1000}
		}, func(err error) bool { return errors.As(err, &UnsafeBinaryError{}) }},
		{"another bypass tool", fstest.MapFS{"proc/1156/stat": {Data: []byte("1156 (nfqws2) S 1 1156 1156 0 -1 4194560 0 0 0 0")}},
			func(*Ops) {}, func(err error) bool { return errors.As(err, &ConflictError{}) }},
		{"no strategy", fstest.MapFS{}, func(o *Ops) {
			if err := config.Save(o.P, config.Default()); err != nil {
				t.Fatal(err)
			}
		}, func(err error) bool { return errors.Is(err, render.ErrNoStrategy) }},
		// The service's run could not start: neither could the run stopped for it.
		{"app the presets dropped", fstest.MapFS{}, func(o *Ops) {
			c, err := config.Load(o.P)
			if err != nil {
				t.Fatal(err)
			}
			c.Apps = []string{"nosuch"}
			if err := config.Save(o.P, c); err != nil {
				t.Fatal(err)
			}
		}, func(err error) bool {
			var unknown render.ErrUnknownApp
			return errors.As(err, &unknown) && unknown.ID == "nosuch"
		}},
		{"no engine", fstest.MapFS{}, func(o *Ops) {
			if err := os.Remove(filepath.Join(o.P.EngineDir, "current")); err != nil {
				t.Fatal(err)
			}
		}, func(err error) bool { return errors.Is(err, ErrEngineMissing) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o, rec := serviceOps(t, sysinfo.Systemd, tc.host)
			installBinPath(t, o)
			tc.setup(o)
			holdRunLock(t, o.P, "background", 4343)
			terminateWith(t, rec, func() error { return nil })
			_, err := o.Enable(context.Background(), func() { rec.add("stop here") })
			if !tc.want(err) {
				t.Fatalf("err %v", err)
			}
			if len(rec.service()) > 0 {
				t.Errorf("calls %q", rec.service())
			}
			if _, mode, held := supervisor.LockInfo(o.P); !held || mode != "background" {
				t.Errorf("the run there is gone: %q %v", mode, held)
			}
			if _, err := os.Stat(o.P.SystemdUnit); !os.IsNotExist(err) {
				t.Errorf("unit written: %v", err)
			}
		})
	}
}

// When registering the service fails, nothing is stopped and the unit goes.
func TestEnableRegisterFailsStopsNothing(t *testing.T) {
	o, rec := serviceOps(t, sysinfo.Systemd, fstest.MapFS{})
	installBinPath(t, o)
	holdRunLock(t, o.P, "background", 4343)
	terminateWith(t, rec, func() error { return nil })
	rec.fail = map[string]error{"systemctl enable splitdpi": errors.New("systemctl: exit status 1")}
	_, err := o.Enable(context.Background(), nil)
	if err == nil || errors.As(err, &TakeoverError{}) {
		t.Fatalf("err %v, want the enable error", err)
	}
	if want := []string{"systemctl daemon-reload", "systemctl enable splitdpi"}; !slices.Equal(rec.service(), want) {
		t.Errorf("calls %q, want %q", rec.service(), want)
	}
	if _, err := os.Stat(o.P.SystemdUnit); !os.IsNotExist(err) {
		t.Errorf("unit left: %v", err)
	}
	if got := savedRunMode(t, o.P); got != config.RunWindow {
		t.Errorf("run mode %q", got)
	}
}

// A service that does not start, or does not run, after the run was
// stopped is removed again, and the run starts again in the background.
func TestEnableStartFailsRestartsRun(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(*serviceRecorder)
		cause error
	}{
		{"start fails", func(r *serviceRecorder) {
			r.fail = map[string]error{"systemctl start splitdpi": errors.New("systemctl: exit status 1")}
		}, nil},
		{"never runs", func(r *serviceRecorder) { r.down = true }, ErrServiceNotStarted},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o, rec := serviceOps(t, sysinfo.Systemd, fstest.MapFS{})
			installBinPath(t, o)
			shortWait(t)
			tc.setup(rec)
			release := holdRunLock(t, o.P, "background", 4343)
			terminateWith(t, rec, func() error { release(); return nil })
			_, err := o.Enable(context.Background(), nil)
			e := takeoverError(t, err)
			if e.Stopped != "background" || e.PID != 4343 || e.Restarted != backgroundPID || e.RestartErr != nil || e.Undo != nil || e.Running != "" {
				t.Errorf("error %+v", e)
			}
			if tc.cause != nil && !errors.Is(err, tc.cause) {
				t.Errorf("err %v, want %v", err, tc.cause)
			}
			want := []string{
				"systemctl daemon-reload", "systemctl enable splitdpi", "hold signals", "SIGTERM 4343", "systemctl start splitdpi",
				"systemctl stop splitdpi", "systemctl disable splitdpi", "systemd-run", "restore signals",
			}
			if !slices.Equal(rec.service(), want) {
				t.Errorf("calls %q, want %q", rec.service(), want)
			}
			if _, err := os.Stat(o.P.SystemdUnit); !os.IsNotExist(err) {
				t.Errorf("unit left: %v", err)
			}
			if got := savedRunMode(t, o.P); got != config.RunWindow {
				t.Errorf("run mode %q, want window", got)
			}
		})
	}
}

// When the run cannot start again either, through systemd-run or detached,
// the error says so.
func TestEnableRestartFails(t *testing.T) {
	o, rec := serviceOps(t, sysinfo.Systemd, fstest.MapFS{})
	installBinPath(t, o)
	rec.fail = map[string]error{
		"systemctl start splitdpi": errors.New("systemctl: exit status 1"),
		"systemd-run":              errors.New("systemd-run: exit status 1"),
	}
	detachErr := errors.New("fork/exec: permission denied")
	o.Detach = func(*exec.Cmd) (<-chan error, error) { return nil, detachErr }
	release := holdRunLock(t, o.P, "background", 4343)
	terminateWith(t, rec, func() error { release(); return nil })
	_, err := o.Enable(context.Background(), nil)
	e := takeoverError(t, err)
	if e.Restarted != 0 || !errors.Is(e.RestartErr, detachErr) || e.Running != "" || e.Stopping != "" {
		t.Errorf("error %+v, want the restart failed", e)
	}
	if !strings.Contains(err.Error(), "could not be restarted") {
		t.Errorf("message %q", err)
	}
}

// slowStopOps can enable the service in place of a background run (pid
// 4343) that holds the lock past the wait for it; waiting once more gives up
// after more. It returns that run's release.
func slowStopOps(t *testing.T, more time.Duration) (*Ops, *serviceRecorder, func()) {
	t.Helper()
	o, rec := serviceOps(t, sysinfo.Systemd, fstest.MapFS{})
	installBinPath(t, o)
	shortStopWait(t, more)
	return o, rec, holdRunLock(t, o.P, "background", 4343)
}

// The bug of the second round: a run that got SIGTERM but held the lock
// past the wait was said to still run, and nothing ran once it had exited.
// It is waited for once more, and started again in the background once it
// is gone.
func TestEnableRunExitsLate(t *testing.T) {
	o, rec, release := slowStopOps(t, 10*time.Second)
	terminateWith(t, rec, func() error { return nil })
	rec.hooks = map[string]func(){"systemctl disable splitdpi": func() { time.AfterFunc(300*time.Millisecond, release) }}
	_, err := o.Enable(context.Background(), nil)
	e := takeoverError(t, err)
	if !errors.Is(err, ErrStopTimeout) || e.Restarted != backgroundPID || e.RestartErr != nil || e.Running != "" || e.Stopping != "" {
		t.Errorf("error %+v, want the background run restarted", e)
	}
	if !strings.Contains(err.Error(), "the background run was restarted in the background (pid 5151)") {
		t.Errorf("message %q", err)
	}
	want := []string{"systemctl daemon-reload", "systemctl enable splitdpi", "hold signals", "SIGTERM 4343", "systemctl disable splitdpi", "systemd-run", "restore signals"}
	if !slices.Equal(rec.service(), want) {
		t.Errorf("calls %q, want %q", rec.service(), want)
	}
}

// A run that got SIGTERM and holds the lock after the second wait too is
// still stopping: it does not run the bypass once it is gone, and nothing
// is started in its place.
func TestEnableRunStillStopping(t *testing.T) {
	o, rec, _ := slowStopOps(t, 300*time.Millisecond)
	terminateWith(t, rec, func() error { return nil })
	_, err := o.Enable(context.Background(), nil)
	e := takeoverError(t, err)
	if e.Stopping != "background" || e.Running != "" || e.Restarted != 0 || e.RestartErr != nil {
		t.Errorf("error %+v, want the background run still stopping", e)
	}
	if !strings.Contains(err.Error(), "the background run is still stopping") {
		t.Errorf("message %q", err)
	}
	want := []string{"systemctl daemon-reload", "systemctl enable splitdpi", "hold signals", "SIGTERM 4343", "systemctl disable splitdpi", "restore signals"}
	if !slices.Equal(rec.service(), want) {
		t.Errorf("calls %q, want %q", rec.service(), want)
	}
}

// A run that took the lock once the stopped one had exited was never told
// to stop: it still runs.
func TestEnableAnotherRunTookLock(t *testing.T) {
	o, rec, release := slowStopOps(t, 300*time.Millisecond)
	terminateWith(t, rec, func() error {
		release()
		holdRunLock(t, o.P, "foreground", 4545)
		return nil
	})
	_, err := o.Enable(context.Background(), nil)
	e := takeoverError(t, err)
	if e.Running != "foreground" || e.Stopping != "" || e.Restarted != 0 || e.RestartErr != nil {
		t.Errorf("error %+v, want the foreground run still running", e)
	}
}

// runit's sv down, like removing the link, only asks runsv to stop the
// service: one that did not run in time may hold the lock a while after
// Enable removed it. It is waited for, not said to still run, and the run it
// was to replace starts again in the background once it is gone.
func TestEnableRunitServiceStopsLate(t *testing.T) {
	o, rec := serviceOps(t, sysinfo.Runit, fstest.MapFS{})
	installBinPath(t, o)
	shortWait(t)
	shortStopWait(t, 10*time.Second)
	rec.mu.Lock()
	rec.down = true
	rec.mu.Unlock()
	o.Detach = func(*exec.Cmd) (<-chan error, error) {
		holdRunLock(t, o.P, "background", backgroundPID)
		return make(chan error), nil
	}
	release := holdRunLock(t, o.P, "background", 4343)
	terminateWith(t, rec, func() error { release(); return nil })
	_, err := o.Enable(context.Background(), nil)
	e := takeoverError(t, err)
	if !errors.Is(err, ErrServiceNotStarted) || e.Restarted != backgroundPID || e.RestartErr != nil || e.Running != "" || e.Stopping != "" {
		t.Errorf("error %+v, want the background run restarted", e)
	}
	if want := []string{"hold signals", "SIGTERM 4343", "sv down splitdpi", "restore signals"}; !slices.Equal(rec.service(), want) {
		t.Errorf("calls %q, want %q", rec.service(), want)
	}
	if _, err := os.Lstat(o.P.RunitLink); !os.IsNotExist(err) {
		t.Errorf("still linked: %v", err)
	}
}

// A run that ends by itself while the service is set up is not signalled:
// its PID may belong to another process by then.
func TestEnableRunEndsMeanwhile(t *testing.T) {
	o, rec := serviceOps(t, sysinfo.Systemd, fstest.MapFS{})
	installBinPath(t, o)
	release := holdRunLock(t, o.P, "background", 4343)
	terminateWith(t, rec, func() error { return nil })
	rec.hooks = map[string]func(){"systemctl enable splitdpi": release}
	if _, err := o.Enable(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	want := []string{"systemctl daemon-reload", "systemctl enable splitdpi", "hold signals", "systemctl start splitdpi", "restore signals"}
	if !slices.Equal(rec.service(), want) {
		t.Errorf("calls %q, want %q", rec.service(), want)
	}
}

// A run that cannot be stopped keeps running: the service goes again and
// nothing is restarted.
func TestEnableStopFails(t *testing.T) {
	o, rec := serviceOps(t, sysinfo.Systemd, fstest.MapFS{})
	installBinPath(t, o)
	holdRunLock(t, o.P, "background", 4343)
	terminateWith(t, rec, func() error { return syscall.ESRCH })
	_, err := o.Enable(context.Background(), nil)
	e := takeoverError(t, err)
	var stop StopRunError
	if !errors.As(err, &stop) || stop.Mode != "background" || stop.PID != 4343 || !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("err %v, want a StopRunError for the background run", err)
	}
	if e.Running != "background" || e.Restarted != 0 {
		t.Errorf("error %+v, want the background run still running", e)
	}
	want := []string{"systemctl daemon-reload", "systemctl enable splitdpi", "hold signals", "SIGTERM 4343", "systemctl disable splitdpi", "restore signals"}
	if !slices.Equal(rec.service(), want) {
		t.Errorf("calls %q, want %q", rec.service(), want)
	}
	if _, err := os.Stat(o.P.SystemdUnit); !os.IsNotExist(err) {
		t.Errorf("unit left: %v", err)
	}
}

// A service that was set up and running before stays as it was when enable
// fails again: undoing takes back only what this enable did.
func TestEnableKeepsServiceItFound(t *testing.T) {
	o, rec := serviceOps(t, sysinfo.Systemd, fstest.MapFS{})
	installBinPath(t, o)
	installAt(t, o.P.SystemdUnit)
	if err := os.MkdirAll(filepath.Dir(o.P.SystemdLink), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(o.P.SystemdUnit, o.P.SystemdLink); err != nil {
		t.Fatal(err)
	}
	holdRunLock(t, o.P, "service", 4242)
	rec.fail = map[string]error{"systemctl daemon-reload": errors.New("systemctl: exit status 1")}
	if _, err := o.Enable(context.Background(), nil); err == nil {
		t.Fatal("no error")
	}
	if want := []string{"systemctl daemon-reload"}; !slices.Equal(rec.service(), want) {
		t.Errorf("calls %q, want %q", rec.service(), want)
	}
	if _, err := os.Stat(o.P.SystemdUnit); err != nil {
		t.Errorf("unit removed: %v", err)
	}
}

// runit starts a service as soon as it is linked, so the link comes only
// after the run has stopped.
func TestEnableRunitLinksAfterStop(t *testing.T) {
	o, rec := serviceOps(t, sysinfo.Runit, fstest.MapFS{})
	installBinPath(t, o)
	release := holdRunLock(t, o.P, "background", 4343)
	terminateWith(t, rec, func() error {
		if _, err := os.Stat(filepath.Join(o.P.RunitDir, "run")); err != nil {
			t.Errorf("service not written before the stop: %v", err)
		}
		if _, err := os.Lstat(o.P.RunitLink); !os.IsNotExist(err) {
			t.Errorf("service linked, so started, before the run stopped: %v", err)
		}
		release()
		return nil
	})
	if _, err := o.Enable(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(o.P.RunitLink); err != nil {
		t.Errorf("not linked: %v", err)
	}
	if want := []string{"hold signals", "SIGTERM 4343", "restore signals"}; !slices.Equal(rec.service(), want) {
		t.Errorf("calls %q, want %q", rec.service(), want)
	}
}

// Without the binary at BinPath (another install.sh prefix), the service of
// every init runs this binary once only root can change it.
func TestEnableCustomPrefix(t *testing.T) {
	const bin = "/opt/splitdpi/bin/splitdpi"
	for in, file := range map[sysinfo.Init]func(paths.Paths) string{
		sysinfo.Systemd: func(p paths.Paths) string { return p.SystemdUnit },
		sysinfo.OpenRC:  func(p paths.Paths) string { return p.OpenRCScript },
		sysinfo.Runit:   func(p paths.Paths) string { return filepath.Join(p.RunitDir, "run") },
	} {
		t.Run(string(in), func(t *testing.T) {
			o, _ := serviceOps(t, in, selfAt(bin))
			if _, err := o.Enable(context.Background(), nil); err != nil {
				t.Fatal(err)
			}
			b, err := os.ReadFile(file(o.P))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(b), bin) || strings.Contains(string(b), o.P.BinPath) {
				t.Errorf("%s does not run %s:\n%s", file(o.P), bin, b)
			}
		})
	}
}

// A binary replaced while it runs: the service runs the one now at its path.
func TestEnableReplacedBinary(t *testing.T) {
	const bin = "/opt/splitdpi/bin/splitdpi"
	host := selfAt(bin)
	host["proc/self/exe"] = &fstest.MapFile{Mode: fs.ModeSymlink, Data: []byte(bin + " (deleted)")}
	o, _ := serviceOps(t, sysinfo.Systemd, host)
	if _, err := o.Enable(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(o.P.SystemdUnit); !strings.Contains(string(b), "ExecStart="+bin+" run --service\n") {
		t.Errorf("unit:\n%s", b)
	}
}

// BinPath, where install.sh puts splitdpi by default, is used as before,
// whatever binary runs enable.
func TestEnableBinPathFirst(t *testing.T) {
	host := selfAt("/home/berk/splitdpi")
	o, _ := serviceOps(t, sysinfo.Systemd, host)
	installBinPath(t, o)
	if _, err := o.Enable(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(o.P.SystemdUnit); !strings.Contains(string(b), "ExecStart="+o.P.BinPath+" run --service\n") {
		t.Errorf("unit:\n%s", b)
	}
}

// A binary someone other than root could replace is refused, and nothing is
// set up: the binary, every directory above it, and where links lead count.
func TestEnableRefusesUnsafeBinary(t *testing.T) {
	const bin = "/opt/splitdpi/bin/splitdpi"
	for _, tc := range []struct {
		name, bin string
		change    func(fstest.MapFS)
		at        string
		reason    string
	}{
		{"binary not root's", bin, func(h fstest.MapFS) { h["opt/splitdpi/bin/splitdpi"].Sys = &syscall.Stat_t{Uid: 1000} }, bin, UnsafeOwner},
		{"directory not root's", bin, func(h fstest.MapFS) { h["opt/splitdpi/bin"].Sys = &syscall.Stat_t{Uid: 1000} }, "/opt/splitdpi/bin", UnsafeOwner},
		{"directory above not root's", bin, func(h fstest.MapFS) { h["opt/splitdpi"].Sys = &syscall.Stat_t{Uid: 1000} }, "/opt/splitdpi", UnsafeOwner},
		{"/ not root's", bin, func(h fstest.MapFS) { h["."].Sys = &syscall.Stat_t{Uid: 1000} }, "/", UnsafeOwner},
		{"owner unknown", bin, func(h fstest.MapFS) { h["opt/splitdpi/bin"].Sys = nil }, "/opt/splitdpi/bin", UnsafeOwner},
		{"binary group-writable", bin, func(h fstest.MapFS) { h["opt/splitdpi/bin/splitdpi"].Mode = 0o775 }, bin, UnsafeWritable},
		{"directory writable by others", bin, func(h fstest.MapFS) { h["opt/splitdpi/bin"].Mode = fs.ModeDir | 0o757 }, "/opt/splitdpi/bin", UnsafeWritable},
		{"directory above group-writable", bin, func(h fstest.MapFS) { h["opt"].Mode = fs.ModeDir | 0o775 }, "/opt", UnsafeWritable},
		{"link into a home", bin, func(h fstest.MapFS) {
			h["opt/splitdpi"] = &fstest.MapFile{Mode: fs.ModeSymlink, Data: []byte("../home/berk/sd")}
			hostTree(h, "/home/berk/sd/bin/splitdpi")
		}, "/home/berk/sd/bin/splitdpi", UnsafeLocation},
		{"in /tmp", "/tmp/sd/splitdpi", func(fstest.MapFS) {}, "/tmp/sd/splitdpi", UnsafeLocation},
		{"in /var/tmp", "/var/tmp/splitdpi", func(fstest.MapFS) {}, "/var/tmp/splitdpi", UnsafeLocation},
		{"in /dev/shm", "/dev/shm/splitdpi", func(fstest.MapFS) {}, "/dev/shm/splitdpi", UnsafeLocation},
		{"in a home", "/home/berk/.local/bin/splitdpi", func(fstest.MapFS) {}, "/home/berk/.local/bin/splitdpi", UnsafeLocation},
		{"in root's home", "/root/bin/splitdpi", func(fstest.MapFS) {}, "/root/bin/splitdpi", UnsafeLocation},
	} {
		t.Run(tc.name, func(t *testing.T) {
			host := selfAt(tc.bin)
			tc.change(host)
			o, rec := serviceOps(t, sysinfo.Systemd, host)
			_, err := o.Enable(context.Background(), nil)
			want := UnsafeBinaryError{Path: tc.bin, At: tc.at, Reason: tc.reason, Install: o.P.BinPath}
			var unsafe UnsafeBinaryError
			if !errors.As(err, &unsafe) || unsafe != want {
				t.Fatalf("err %v, want %+v", err, want)
			}
			if len(rec.service()) > 0 {
				t.Errorf("calls %q", rec.service())
			}
			if _, err := os.Stat(o.P.SystemdUnit); !os.IsNotExist(err) {
				t.Errorf("unit written: %v", err)
			}
		})
	}
}

// BinPath is checked like any other binary: a user may own it or a
// directory above it when install.sh ran without sudo.
func TestEnableChecksBinPath(t *testing.T) {
	const bin = "/usr/local/bin/splitdpi"
	for _, tc := range []struct {
		name, at, reason string
		change           func(fstest.MapFS)
	}{
		{"binary not root's", bin, UnsafeOwner, func(h fstest.MapFS) { h["usr/local/bin/splitdpi"].Sys = &syscall.Stat_t{Uid: 1000} }},
		{"directory above not root's", "/usr/local", UnsafeOwner, func(h fstest.MapFS) { h["usr/local"].Sys = &syscall.Stat_t{Uid: 1000} }},
		{"directory group-writable", "/usr/local/bin", UnsafeWritable, func(h fstest.MapFS) { h["usr/local/bin"].Mode = fs.ModeDir | 0o775 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o, rec := serviceOps(t, sysinfo.Systemd, fstest.MapFS{})
			installBinPath(t, o)
			tc.change(o.Root.(fstest.MapFS))
			_, err := o.Enable(context.Background(), nil)
			want := UnsafeBinaryError{Path: o.P.BinPath, At: tc.at, Reason: tc.reason, Install: o.P.BinPath}
			var unsafe UnsafeBinaryError
			if !errors.As(err, &unsafe) || unsafe != want {
				t.Fatalf("err %v, want %+v", err, want)
			}
			if len(rec.service()) > 0 {
				t.Errorf("calls %q", rec.service())
			}
		})
	}
}

// Links are followed as the kernel follows them, relative ones with ".."
// too: Fedora's atomic desktops link /usr/local to ../var/usrlocal.
func TestUnsafeReasonFollowsLinks(t *testing.T) {
	const bin = "/usr/local/bin/splitdpi"
	host := fstest.MapFS{}
	hostTree(host, "/var/usrlocal/bin/splitdpi")
	host["usr"] = rootDir(0o755)
	host["usr/local"] = &fstest.MapFile{Mode: fs.ModeSymlink, Data: []byte("../var/usrlocal")}
	if at, reason, err := unsafeReason(host, bin); reason != "" || err != nil {
		t.Errorf("root's layout behind a link: %q at %q, %v", reason, at, err)
	}
	host["var/usrlocal/bin"].Sys = &syscall.Stat_t{Uid: 1000}
	if at, reason, err := unsafeReason(host, bin); reason != UnsafeOwner || at != "/var/usrlocal/bin" || err != nil {
		t.Errorf("a user's directory behind a link: %q at %q, %v", reason, at, err)
	}
	host["usr/local"].Data = []byte("/usr/local")
	if _, _, err := unsafeReason(host, bin); !errors.Is(err, syscall.ELOOP) {
		t.Errorf("a link loop: %v", err)
	}
}

// With no binary at BinPath and none found running, enable asks for BinPath.
func TestEnableNoBinary(t *testing.T) {
	gone := selfAt("/opt/splitdpi/bin/splitdpi")
	delete(gone, "opt/splitdpi/bin/splitdpi")
	for name, host := range map[string]fstest.MapFS{"no /proc/self/exe": {}, "binary gone": gone} {
		t.Run(name, func(t *testing.T) {
			o, _ := serviceOps(t, sysinfo.Systemd, host)
			_, err := o.Enable(context.Background(), nil)
			var missing BinaryMissingError
			if !errors.As(err, &missing) || missing.Path != o.P.BinPath {
				t.Errorf("err %v, want BinPath missing", err)
			}
		})
	}
}

// signalMasks are the signals the kernel has this process ignore and catch.
func signalMasks(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("/proc/self/status")
	if err != nil {
		t.Skip(err)
	}
	var masks []string
	for l := range strings.Lines(string(b)) {
		if strings.HasPrefix(l, "SigIgn:") || strings.HasPrefix(l, "SigCgt:") {
			masks = append(masks, strings.TrimSpace(l))
		}
	}
	return strings.Join(masks, " ")
}

// After the takeover, SIGHUP and SIGINT must end the TUI again, or a later
// window run would outlive its window. signal.Ignore cannot be undone:
// signal.Ignored says it is, the kernel keeps ignoring. No signal is sent.
func TestHoldSignalsRestores(t *testing.T) {
	before := signalMasks(t)
	holdSignals()()
	if after := signalMasks(t); after != before {
		t.Errorf("signals after the hold:\n%s\nbefore:\n%s", after, before)
	}
}

func TestServiceState(t *testing.T) {
	o := &Ops{P: paths.Under(t.TempDir()), Root: fstest.MapFS{}}
	if got := o.ServiceState(); got != (ServiceState{}) {
		t.Errorf("unsupported init: %+v", got)
	}
	o.Root = initHosts[sysinfo.Systemd]
	if got := o.ServiceState(); got != (ServiceState{Supported: true}) {
		t.Errorf("nothing installed: %+v", got)
	}
	installAt(t, o.P.SystemdUnit)
	if got := o.ServiceState(); got != (ServiceState{Supported: true, Installed: true}) {
		t.Errorf("unit only: %+v", got)
	}
	if err := os.MkdirAll(filepath.Dir(o.P.SystemdLink), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc/systemd/system/splitdpi.service", o.P.SystemdLink); err != nil {
		t.Fatal(err)
	}
	if got := o.ServiceState(); got != (ServiceState{Supported: true, Installed: true, Enabled: true}) {
		t.Errorf("enabled: %+v", got)
	}
}
