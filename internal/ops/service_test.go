package ops

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"testing/fstest"

	"github.com/KadirBerkpolat1/SplitDPI/internal/config"
	"github.com/KadirBerkpolat1/SplitDPI/internal/paths"
	"github.com/KadirBerkpolat1/SplitDPI/internal/supervisor"
	"github.com/KadirBerkpolat1/SplitDPI/internal/sysinfo"
)

// Every command here is faked and TerminateRun is replaced: no real service,
// table or process is touched.

// holdRunLock makes the test process look like a supervisor with pid in mode;
// the returned func releases the lock, like that supervisor exiting.
func holdRunLock(t *testing.T, p paths.Paths, mode string, pid int) func() {
	t.Helper()
	if err := os.MkdirAll(p.RunDir, 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(p.LockFile, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprintf(f, `{"pid":%d,"mode":%q}`, pid, mode)
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	st := fmt.Sprintf(`{"state":"running","mode":%q,"pid":%d}`, mode, pid)
	if err := os.WriteFile(p.StatusFile, []byte(st), 0o644); err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	release := func() { once.Do(func() { f.Close() }) }
	t.Cleanup(release)
	return release
}

// serviceRecorder keeps the commands run and the PIDs signalled, in one order.
type serviceRecorder struct {
	mu    sync.Mutex
	calls []string
}

func (r *serviceRecorder) add(s string) {
	r.mu.Lock()
	r.calls = append(r.calls, s)
	r.mu.Unlock()
}

// service is the calls that change the service or a run, in order.
func (r *serviceRecorder) service() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.DeleteFunc(slices.Clone(r.calls), func(c string) bool { return strings.HasPrefix(c, "lsattr ") })
}

var initHosts = map[sysinfo.Init]fstest.MapFS{
	sysinfo.Systemd: {"run/systemd/system": {Mode: fs.ModeDir}},
	sysinfo.OpenRC:  {"run/openrc": {Mode: fs.ModeDir}},
	sysinfo.Runit:   {"etc/runit": {Mode: fs.ModeDir}, "var/service": {Mode: fs.ModeDir}},
}

// serviceOps can enable the service of init in with host as the host: it has
// a strategy, and starting the service makes it hold the lock and run.
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
	rec := &serviceRecorder{}
	o := &Ops{
		P: p, Root: host, Euid: func() int { return 0 }, GOARCH: "amd64",
		LookPath: func(name string) (string, error) { return "/usr/bin/" + name, nil },
		Exec: func(_ context.Context, _ string, name string, args ...string) ([]byte, error) {
			cmd := strings.Join(append([]string{name}, args...), " ")
			rec.add(cmd)
			switch cmd {
			case "systemctl start splitdpi", "rc-service splitdpi start", "sv up splitdpi":
				holdRunLock(t, p, "service", 4242)
			}
			return nil, nil
		},
	}
	return o, rec
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

func installAt(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func savedRunMode(t *testing.T, p paths.Paths) config.RunMode {
	t.Helper()
	c, err := config.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	return c.RunMode
}

var enableCalls = []string{"systemctl daemon-reload", "systemctl enable splitdpi", "systemctl start splitdpi"}

// A background or foreground run is stopped, and has exited, before the
// service is set up in its place.
func TestEnableTakesOverRun(t *testing.T) {
	for _, mode := range []string{"background", "foreground"} {
		t.Run(mode, func(t *testing.T) {
			o, rec := serviceOps(t, sysinfo.Systemd, fstest.MapFS{})
			installAt(t, o.P.BinPath)
			release := holdRunLock(t, o.P, mode, 4343)
			terminateWith(t, rec, func() error { release(); return nil })
			r, err := o.Enable(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if r != (EnableReport{Stopped: mode, PID: 4343}) {
				t.Errorf("report %+v", r)
			}
			if want := append([]string{"SIGTERM 4343"}, enableCalls...); !slices.Equal(rec.service(), want) {
				t.Errorf("calls %q, want %q", rec.service(), want)
			}
			if got := savedRunMode(t, o.P); got != config.RunBoot {
				t.Errorf("run mode %q, want boot", got)
			}
		})
	}
}

func TestEnableNothingToTakeOver(t *testing.T) {
	o, rec := serviceOps(t, sysinfo.Systemd, fstest.MapFS{})
	installAt(t, o.P.BinPath)
	terminateWith(t, rec, func() error { return nil })
	r, err := o.Enable(context.Background())
	if err != nil || r != (EnableReport{}) {
		t.Fatalf("report %+v, err %v", r, err)
	}
	if !slices.Equal(rec.service(), enableCalls) {
		t.Errorf("calls %q", rec.service())
	}
}

// A scan, and a run of this very process, are not stopped: the service is
// left alone and so is the run mode.
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
			installAt(t, o.P.BinPath)
			holdRunLock(t, o.P, tc.mode, tc.pid)
			terminateWith(t, rec, func() error { return nil })
			_, err := o.Enable(context.Background())
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

func TestEnableStopFails(t *testing.T) {
	o, rec := serviceOps(t, sysinfo.Systemd, fstest.MapFS{})
	installAt(t, o.P.BinPath)
	holdRunLock(t, o.P, "background", 4343)
	terminateWith(t, rec, func() error { return syscall.ESRCH })
	_, err := o.Enable(context.Background())
	var stop StopRunError
	if !errors.As(err, &stop) || stop.Mode != "background" || stop.PID != 4343 || !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("err %v, want a StopRunError for the background run", err)
	}
	if !slices.Equal(rec.service(), []string{"SIGTERM 4343"}) {
		t.Errorf("calls %q", rec.service())
	}
	if _, err := os.Stat(o.P.SystemdUnit); !os.IsNotExist(err) {
		t.Errorf("unit written: %v", err)
	}
}

// rootFile is a host file or directory in root's hands, mode perm.
func rootFile(perm fs.FileMode) *fstest.MapFile {
	return &fstest.MapFile{Mode: perm, Sys: &syscall.Stat_t{Uid: 0}}
}

func rootDir(perm fs.FileMode) *fstest.MapFile {
	return &fstest.MapFile{Mode: fs.ModeDir | perm, Sys: &syscall.Stat_t{Uid: 0}}
}

// selfAt is a host whose running splitdpi is bin, root's like its directory.
func selfAt(bin string) fstest.MapFS {
	return fstest.MapFS{
		"proc/self/exe":                            {Mode: fs.ModeSymlink, Data: []byte(bin)},
		strings.TrimPrefix(bin, "/"):               rootFile(0o755),
		strings.TrimPrefix(filepath.Dir(bin), "/"): rootDir(0o755),
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
			if _, err := o.Enable(context.Background()); err != nil {
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
	if _, err := o.Enable(context.Background()); err != nil {
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
	installAt(t, o.P.BinPath)
	if _, err := o.Enable(context.Background()); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(o.P.SystemdUnit); !strings.Contains(string(b), "ExecStart="+o.P.BinPath+" run --service\n") {
		t.Errorf("unit:\n%s", b)
	}
}

// A binary someone other than root could replace is refused, and nothing is
// set up.
func TestEnableRefusesUnsafeBinary(t *testing.T) {
	const bin = "/opt/splitdpi/bin/splitdpi"
	for _, tc := range []struct {
		name, bin string
		change    func(fstest.MapFS)
		reason    string
	}{
		{"binary not root's", bin, func(h fstest.MapFS) { h["opt/splitdpi/bin/splitdpi"].Sys = &syscall.Stat_t{Uid: 1000} }, UnsafeOwner},
		{"directory not root's", bin, func(h fstest.MapFS) { h["opt/splitdpi/bin"].Sys = &syscall.Stat_t{Uid: 1000} }, UnsafeOwner},
		{"owner unknown", bin, func(h fstest.MapFS) { h["opt/splitdpi/bin"].Sys = nil }, UnsafeOwner},
		{"binary group-writable", bin, func(h fstest.MapFS) { h["opt/splitdpi/bin/splitdpi"].Mode = 0o775 }, UnsafeWritable},
		{"directory writable by others", bin, func(h fstest.MapFS) { h["opt/splitdpi/bin"].Mode = fs.ModeDir | 0o757 }, UnsafeWritable},
		{"in /tmp", "/tmp/sd/splitdpi", func(fstest.MapFS) {}, UnsafeLocation},
		{"in a home", "/home/berk/.local/bin/splitdpi", func(fstest.MapFS) {}, UnsafeLocation},
		{"in root's home", "/root/bin/splitdpi", func(fstest.MapFS) {}, UnsafeLocation},
	} {
		t.Run(tc.name, func(t *testing.T) {
			host := selfAt(tc.bin)
			tc.change(host)
			o, rec := serviceOps(t, sysinfo.Systemd, host)
			_, err := o.Enable(context.Background())
			want := UnsafeBinaryError{Path: tc.bin, Reason: tc.reason, Install: o.P.BinPath}
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

// With no binary at BinPath and none found running, enable asks for BinPath.
func TestEnableNoBinary(t *testing.T) {
	gone := selfAt("/opt/splitdpi/bin/splitdpi")
	delete(gone, "opt/splitdpi/bin/splitdpi")
	for name, host := range map[string]fstest.MapFS{"no /proc/self/exe": {}, "binary gone": gone} {
		t.Run(name, func(t *testing.T) {
			o, _ := serviceOps(t, sysinfo.Systemd, host)
			_, err := o.Enable(context.Background())
			var missing BinaryMissingError
			if !errors.As(err, &missing) || missing.Path != o.P.BinPath {
				t.Errorf("err %v, want BinPath missing", err)
			}
		})
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
