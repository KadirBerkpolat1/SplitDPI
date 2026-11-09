package ops

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/KadirBerkpolat1/SplitDPI/internal/config"
	"github.com/KadirBerkpolat1/SplitDPI/internal/initsys"
	"github.com/KadirBerkpolat1/SplitDPI/internal/render"
	"github.com/KadirBerkpolat1/SplitDPI/internal/supervisor"
	"github.com/KadirBerkpolat1/SplitDPI/internal/sysinfo"
)

var (
	ErrNotEnabled        = errors.New("service not enabled")
	ErrServiceNotStarted = errors.New("service did not reach running within 10 s")
	ErrNoLogs            = errors.New("no service log yet")
)

// BinaryMissingError means the service would point at a binary that is not there.
type BinaryMissingError struct{ Path string }

func (e BinaryMissingError) Error() string { return "no splitdpi binary at " + e.Path }

// UnsafeBinaryError means the service, which root runs at every boot, would
// run a binary at Path that someone other than root could replace, because
// of At: the binary itself or a directory on the way to it. Install is where
// to install it instead; when Path is Install already, At needs fixing.
// Reason is one of UnsafeOwner, UnsafeWritable and UnsafeLocation.
type UnsafeBinaryError struct {
	Path, At, Reason, Install string
}

// Why a binary is not safe for the service.
const (
	UnsafeOwner    = "owner"    // At is not root's
	UnsafeWritable = "writable" // a group or others may write to At
	UnsafeLocation = "location" // At is in a temporary or home directory
)

func (e UnsafeBinaryError) Error() string {
	return fmt.Sprintf("%s is not safe to run as the service (%s: %s)", e.Path, e.At, e.Reason)
}

// StopRunError means Enable could not stop the run the service was to replace.
type StopRunError struct {
	Mode string
	PID  int
	Err  error
}

func (e StopRunError) Error() string {
	return fmt.Sprintf("could not stop the %s run (pid %d): %v", e.Mode, e.PID, e.Err)
}

func (e StopRunError) Unwrap() error { return e.Err }

// EnableReport is the run whose place the service took: Stopped is its mode,
// "" if none ran or the service did not take over, and PID its supervisor.
// The bypass this process ran is "foreground", with this process's PID.
type EnableReport struct {
	Stopped string
	PID     int
}

// TakeoverError means the service Enable set up did not start in place of
// the bypass running before it, for Err, so Enable removed it again; Undo is
// why that failed, if it did. When Enable had stopped a run for the service
// (Stopped, PID), the bypass runs again as the background run Restarted that
// Enable started in its place once no run held the lock; failing that,
// RestartErr says why, and no bypass runs. Running is the mode of a run that
// holds the lock and was never told to stop: it still runs. Stopping is the
// mode of one told to stop, the run Enable stopped or the service, that
// still held it after the wait: it is on its way down, and no bypass runs
// once it is gone.
type TakeoverError struct {
	Stopped    string
	PID        int
	Err        error
	Undo       error
	Running    string
	Stopping   string
	Restarted  int
	RestartErr error
}

func (e TakeoverError) Error() string {
	msg := e.Err.Error()
	if e.Undo != nil {
		msg += "; removing the service failed: " + e.Undo.Error()
	}
	switch {
	case e.Stopped == "":
	case e.Running != "":
		msg += "; the " + e.Running + " run still runs"
	case e.Stopping != "":
		msg += "; the " + e.Stopping + " run is still stopping"
	case e.RestartErr != nil:
		msg += "; the " + e.Stopped + " run could not be restarted: " + e.RestartErr.Error()
	default:
		msg += fmt.Sprintf("; the %s run was restarted in the background (pid %d)", e.Stopped, e.Restarted)
	}
	return msg
}

func (e TakeoverError) Unwrap() error { return e.Err }

// TrimPeriod is s, the text of an error, without the period it may end with,
// for a message that ends the sentence itself: the error of a failed command
// keeps its stderr, which often ends with one.
func TrimPeriod(s string) string { return strings.TrimRight(s, ".") }

// TerminateRun sends SIGTERM to the supervisor of a run, which then stops and
// removes its rules. Tests replace it so that no real process is signalled.
var TerminateRun = func(pid int) error { return syscall.Kill(pid, syscall.SIGTERM) }

// holdSignals keeps SIGHUP (the terminal closing), SIGINT (Ctrl+C) and
// SIGTERM from ending this process until the returned func restores them.
// They are caught, not ignored: signal.Ignore cannot be undone (signal.Reset
// leaves SIGHUP and SIGINT ignored), and it would drop the TUI's own
// handlers. A SIGHUP caught meanwhile ends the process once restored, as it
// would have: its terminal is gone. Tests replace it.
var holdSignals = func() (restore func()) {
	hup := make(chan os.Signal, 1)
	signal.Notify(hup, syscall.SIGHUP)
	dropped := make(chan os.Signal, 1)
	signal.Notify(dropped, syscall.SIGINT, syscall.SIGTERM)
	return func() {
		signal.Stop(dropped)
		signal.Stop(hup)
		if len(hup) > 0 {
			_ = syscall.Kill(os.Getpid(), syscall.SIGHUP)
		}
	}
}

var enableWait = 10 * time.Second

// manager is the service manager of this machine's init, for files under P.
func (o *Ops) manager() (initsys.Manager, error) { return o.managerFor(o.P.BinPath) }

// managerFor is manager for a service that runs bin.
func (o *Ops) managerFor(bin string) (initsys.Manager, error) {
	return initsys.For(sysinfo.DetectInit(o.Root), o.Exec, bin, o.P)
}

// Enable installs the service, makes the init start it at boot and starts it
// in place of the bypass running now, then saves run mode boot. stopHere, if
// not nil, stops the bypass this process runs (the TUI's window) and returns
// once it has exited; another process's background or foreground run gets
// SIGTERM. Enable goes in four steps, so that the bypass is down only
// between the third and the fourth, with the signals that would end this
// process held there:
//
//  1. Every check, with nothing changed yet, the service's own run being
//     able to start among them.
//  2. The service is installed and registered for boot, not started.
//  3. The bypass running now stops and lets go of the lock.
//  4. The service starts and runs.
//
// A failure in step 1 changes nothing. A later one removes the service
// again and keeps the run mode: a TakeoverError says so, and whether the
// bypass stopped in step 3 was started again in the background.
func (o *Ops) Enable(ctx context.Context, stopHere func()) (EnableReport, error) {
	cfg, err := config.Load(o.P)
	if err != nil {
		return EnableReport{}, err
	}
	if cfg.Strategy == nil {
		return EnableReport{}, render.ErrNoStrategy
	}
	// Without a service on this init, the binary does not matter.
	if _, err := o.manager(); err != nil {
		return EnableReport{}, err
	}
	bin, err := o.serviceBinary()
	if err != nil {
		return EnableReport{}, err
	}
	m, err := o.managerFor(bin)
	if err != nil {
		return EnableReport{}, err
	}
	if err := o.refuseConflicts(ctx); err != nil {
		return EnableReport{}, err
	}
	// A config no run starts with would stop the bypass for a service that fails.
	if _, err := o.prepare(cfg, "service", io.Discard, nil); err != nil {
		return EnableReport{}, err
	}
	r, err := o.runToReplace(stopHere)
	if err != nil {
		return EnableReport{}, err
	}

	// What changes from here on is finished or undone, even once ctx is done.
	ctx = context.WithoutCancel(ctx)
	s := o.newSetup(m)
	if err := s.register(ctx); err != nil {
		return EnableReport{}, err
	}
	if r.Stopped == "" {
		if err := o.startService(ctx, s); err != nil {
			return EnableReport{}, TakeoverError{Err: err, Undo: s.undo(ctx)}
		}
		return r, o.SetRunMode(config.RunBoot)
	}
	restore := holdSignals()
	defer restore()
	told, err := o.stopForService(r, stopHere)
	if err == nil {
		err = o.startService(ctx, s)
	}
	if err != nil {
		return EnableReport{}, o.takeBack(ctx, s, r, told, err)
	}
	return r, o.SetRunMode(config.RunBoot)
}

// runToReplace is the bypass running now that the service is to take the
// place of: the one stopHere stops, or another process's background or
// foreground run. A scan, and a run of this process that stopHere does not
// stop, must not be stopped: ErrAlreadyRunning.
func (o *Ops) runToReplace(stopHere func()) (EnableReport, error) {
	pid, mode, held := supervisor.LockInfo(o.P)
	here := pid == os.Getpid()
	switch {
	case stopHere != nil && (!held || here && mode == "foreground"):
		return EnableReport{Stopped: "foreground", PID: os.Getpid()}, nil
	case !held || mode == "service":
		return EnableReport{}, nil
	case !here && (mode == "background" || mode == "foreground"):
		return EnableReport{Stopped: mode, PID: pid}, nil
	}
	return EnableReport{}, supervisor.ErrAlreadyRunning{Mode: mode}
}

// stopForService stops the run r so that the service can take its place:
// through stopHere, and with SIGTERM to another process's supervisor, if
// that still holds the lock: it may have ended since, and its PID gone to
// another process. Then it waits until no live process holds the lock.
// told is whether r was told to stop.
func (o *Ops) stopForService(r EnableReport, stopHere func()) (told bool, err error) {
	if stopHere != nil {
		stopHere()
		told = true
	}
	if pid, _, held := supervisor.LockInfo(o.P); held && pid == r.PID && pid != os.Getpid() {
		if err := TerminateRun(pid); err != nil {
			return false, StopRunError{Mode: r.Stopped, PID: pid, Err: err}
		}
		told = true
	}
	if !o.waitReleased() {
		return told, StopRunError{Mode: r.Stopped, PID: r.PID, Err: ErrStopTimeout}
	}
	return told, nil
}

// startService starts the service and waits until it runs.
func (o *Ops) startService(ctx context.Context, s *serviceSetup) error {
	s.started = true
	if err := s.m.Start(ctx); err != nil {
		return err
	}
	if _, ok := o.waitRunning("service", nil); !ok {
		return ErrServiceNotStarted
	}
	return nil
}

// takeBack removes the service that did not take the place of the run r,
// for cause, and starts that run again in the background once no live
// process holds the lock. A run told to stop, r if told and the service if
// started, may take a while to exit: takeBack waits up to takeBackWait for
// it. Any other run holding the lock was never told to stop and still runs:
// r when its stop failed, or one that took the lock since.
func (o *Ops) takeBack(ctx context.Context, s *serviceSetup, r EnableReport, told bool, cause error) error {
	e := TakeoverError{Stopped: r.Stopped, PID: r.PID, Err: cause, Undo: s.undo(ctx)}
	deadline := time.Now().Add(takeBackWait)
	for {
		pid, mode, held := supervisor.LockInfo(o.P)
		stopping := told && pid == r.PID || s.started && mode == "service"
		switch {
		case !held:
			e.Restarted, e.RestartErr = o.Background(ctx)
			return e
		case !stopping:
			e.Running = mode
			return e
		case time.Now().After(deadline):
			e.Stopping = mode
			return e
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// serviceSetup is the service as Enable changes it, so that a failure can
// put back what was there before: a service Enable installed is removed,
// one it registered for boot is unregistered, one it started is stopped.
type serviceSetup struct {
	m          initsys.Manager
	was        ServiceState // before Enable
	wasRunning bool
	registered bool // the init starts it at boot because of Enable
	started    bool // Enable asked the init to start it
}

func (o *Ops) newSetup(m initsys.Manager) *serviceSetup {
	_, mode, held := supervisor.LockInfo(o.P)
	return &serviceSetup{
		m:          m,
		was:        ServiceState{Supported: true, Installed: m.Installed(), Enabled: m.Enabled()},
		wasRunning: held && mode == "service",
	}
}

// register installs the service and registers it for boot without starting
// it. A failure undoes both.
func (s *serviceSetup) register(ctx context.Context) error {
	err := s.m.Install()
	if err == nil {
		err = s.m.Enable(ctx)
	}
	if err != nil {
		if uerr := s.undo(ctx); uerr != nil {
			err = errors.Join(err, uerr)
		}
		return err
	}
	s.registered = true
	return nil
}

// undo puts the service back as Enable found it.
func (s *serviceSetup) undo(ctx context.Context) error {
	if s.started && !s.wasRunning {
		_ = s.m.Stop(ctx) // it may never have come up; the rest still matters
	}
	var errs []error
	if s.registered && !s.was.Enabled {
		errs = append(errs, s.m.Disable(ctx))
	}
	if !s.was.Installed {
		if err := s.m.Remove(); err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// releaseWait is how long a run told to stop has to let go of the lock;
// takeBackWait is how much longer Enable waits for one before it gives up
// starting the bypass again in its place.
var releaseWait, takeBackWait = 10 * time.Second, 20 * time.Second

// waitReleased waits up to releaseWait until no live process holds the lock.
func (o *Ops) waitReleased() bool {
	deadline := time.Now().Add(releaseWait)
	for {
		if _, _, held := supervisor.LockInfo(o.P); !held {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// serviceBinary is the splitdpi the service runs, provided only root can
// change it: BinPath, where install.sh puts it by default, as always; else
// this binary. /proc/self/exe is what os.Executable reads on Linux, the path
// with every symlink resolved; like the rest of the host it is read through
// Root.
func (o *Ops) serviceBinary() (string, error) {
	if _, err := os.Stat(o.P.BinPath); err == nil {
		return o.P.BinPath, o.checkBinary(o.P.BinPath, o.onHost(o.P.BinPath))
	}
	self, err := fs.ReadLink(o.Root, "proc/self/exe")
	if err != nil {
		return "", BinaryMissingError{Path: o.P.BinPath}
	}
	// A binary replaced while it runs reads as "PATH (deleted)": PATH is the new one.
	self = filepath.Clean(strings.TrimSuffix(self, " (deleted)"))
	if err := o.checkBinary(self, self); errors.Is(err, fs.ErrNotExist) {
		return "", BinaryMissingError{Path: o.P.BinPath}
	} else if err != nil {
		return "", err
	}
	return self, nil
}

// checkBinary refuses bin as the service's binary unless only root can
// change it; host is bin as the host has it.
func (o *Ops) checkBinary(bin, host string) error {
	at, reason, err := unsafeReason(o.Root, host)
	switch {
	case err != nil:
		return err
	case reason != "":
		return UnsafeBinaryError{Path: bin, At: at, Reason: reason, Install: o.P.BinPath}
	}
	return nil
}

// onHost is path, one of P's, as the host has it: P.Root is the host's "/"
// unless SPLITDPI_ROOT moves splitdpi's files elsewhere, as tests do.
func (o *Ops) onHost(path string) string {
	rel, err := filepath.Rel(o.P.Root, path)
	if err != nil {
		return path
	}
	return filepath.Join("/", rel)
}

// unsafeDirs keep temporary files or a user's files: home directories,
// including where Fedora's atomic desktops keep them.
var unsafeDirs = []string{"/tmp", "/var/tmp", "/dev/shm", "/home", "/var/home", "/root", "/var/roothome"}

func inUnsafeDir(path string) bool {
	return slices.ContainsFunc(unsafeDirs, func(d string) bool { return strings.HasPrefix(path, d+"/") })
}

// unsafeReason says why root must not run bin, a host path read through
// root, at every boot, and what at: bin or a directory on the way to it. It
// is "" when only root can change bin: bin and every directory the kernel
// passes through to reach it, from "/" and along symbolic links, are root's
// and not writable by a group or others, and neither bin nor where it leads
// is in a temporary or home directory.
func unsafeReason(root fs.FS, bin string) (at, reason string, err error) {
	if inUnsafeDir(bin) {
		return bin, UnsafeLocation, nil
	}
	fi, err := fs.Stat(root, ".")
	if err != nil {
		return "", "", err
	}
	if reason := modeReason(fi); reason != "" {
		return "/", reason, nil
	}
	dir, rest := "/", strings.Split(bin, "/")
	for links := 0; len(rest) > 0; {
		name := rest[0]
		rest = rest[1:]
		switch name {
		case "", ".":
			continue
		case "..":
			dir = filepath.Dir(dir)
			continue
		}
		p := filepath.Join(dir, name)
		// Joined to ".", a host path is a path in root.
		fi, err := fs.Lstat(root, filepath.Join(".", p))
		if err != nil {
			return "", "", err
		}
		if fi.Mode()&fs.ModeSymlink != 0 {
			if links++; links > 40 {
				return "", "", &fs.PathError{Op: "stat", Path: bin, Err: syscall.ELOOP}
			}
			target, err := fs.ReadLink(root, filepath.Join(".", p))
			if err != nil {
				return "", "", err
			}
			if filepath.IsAbs(target) {
				dir = "/"
			}
			rest = append(strings.Split(target, "/"), rest...)
			continue
		}
		if reason := modeReason(fi); reason != "" {
			return p, reason, nil
		}
		dir = p
	}
	if inUnsafeDir(dir) {
		return dir, UnsafeLocation, nil
	}
	return "", "", nil
}

// modeReason is why someone other than root could change fi, "" if no one can.
func modeReason(fi fs.FileInfo) string {
	if st, ok := fi.Sys().(*syscall.Stat_t); !ok || st.Uid != 0 {
		return UnsafeOwner
	}
	if fi.Mode().Perm()&0o022 != 0 {
		return UnsafeWritable
	}
	return ""
}

// waitRunning waits up to enableWait for a supervisor in mode to hold the
// lock and report running, and returns its PID. It gives up early when
// exited delivers: the process that was to run is gone.
func (o *Ops) waitRunning(mode string, exited <-chan error) (int, bool) {
	deadline := time.Now().Add(enableWait)
	for time.Now().Before(deadline) {
		pid, m, held := supervisor.LockInfo(o.P)
		if st, _ := supervisor.ReadStatus(o.P); held && m == mode && st.State == "running" {
			return pid, true
		}
		select {
		case <-exited:
			return 0, false
		case <-time.After(100 * time.Millisecond):
		}
	}
	return 0, false
}

// Disable stops, disables and removes the service, and run mode boot becomes
// the default: nothing starts SplitDPI at boot any more.
func (o *Ops) Disable(ctx context.Context) error {
	m, err := o.manager()
	if err != nil {
		return err
	}
	if !m.Installed() {
		if err := o.leaveBootMode(); err != nil {
			return err
		}
		return ErrNotEnabled
	}
	if err := removeService(ctx, m); err != nil {
		return err
	}
	return o.leaveBootMode()
}

func removeService(ctx context.Context, m initsys.Manager) error {
	// Stop may fail when the service is not running; disabling still matters.
	stopErr := m.Stop(ctx)
	if err := m.Disable(ctx); err != nil {
		return errors.Join(stopErr, err)
	}
	return m.Remove()
}

// Logs writes the service log to w: the journal on systemd, else LogFile
// (after LogFile.1). A background run writes LogFile on every init, so while
// it is the current or last run, Logs reads LogFile. follow keeps writing new
// lines until ctx is done.
func (o *Ops) Logs(ctx context.Context, follow bool, w io.Writer) error {
	if m, err := o.manager(); err == nil && o.lastMode() != "background" {
		if cmd := m.LogsCmd(follow); cmd != nil {
			if !follow {
				out, err := o.Exec(ctx, "", cmd[0], cmd[1:]...)
				_, werr := w.Write(out)
				return errors.Join(err, werr)
			}
			c := exec.CommandContext(ctx, cmd[0], cmd[1:]...)
			c.Stdout, c.Stderr = w, w
			return c.Run()
		}
	}
	found := false
	for _, f := range []string{o.P.LogFile + ".1", o.P.LogFile} {
		if b, err := os.ReadFile(f); err == nil {
			if _, err := w.Write(b); err != nil {
				return err
			}
			found = true
		}
	}
	if !follow {
		if !found {
			return ErrNoLogs
		}
		return nil
	}
	return tailLog(ctx, o.P.LogFile, w)
}

// lastMode is the mode of the supervisor holding the lock, else of the last
// one that wrote status.
func (o *Ops) lastMode() string {
	if _, mode, held := supervisor.LockInfo(o.P); held {
		return mode
	}
	st, _ := supervisor.ReadStatus(o.P)
	return st.Mode
}

// tailLog writes what is appended to path until ctx is done, starting over
// when the file is rotated.
func tailLog(ctx context.Context, path string, w io.Writer) error {
	var off int64
	if fi, err := os.Stat(path); err == nil {
		off = fi.Size()
	}
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
		}
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		if fi, err := f.Stat(); err == nil && fi.Size() < off {
			off = 0
		}
		n, err := f.Seek(off, io.SeekStart)
		if err == nil {
			copied, _ := io.Copy(w, f)
			off = n + copied
		}
		_ = f.Close() // only read: closing it loses nothing
	}
}

func serviceLabel(o *Ops) string {
	return fmt.Sprintf("%s service (%s)", o.P.NftTable, sysinfo.DetectInit(o.Root))
}
