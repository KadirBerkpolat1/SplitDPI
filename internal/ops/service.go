package ops

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
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
// run a binary at Path that someone other than root could replace; Install
// is where to install it instead. Reason is one of UnsafeOwner,
// UnsafeWritable and UnsafeLocation.
type UnsafeBinaryError struct {
	Path, Reason, Install string
}

// Why a binary is not safe for the service.
const (
	UnsafeOwner    = "owner"    // it or its directory is not root's
	UnsafeWritable = "writable" // a group or others may write to it or its directory
	UnsafeLocation = "location" // it is in /tmp or a home directory
)

func (e UnsafeBinaryError) Error() string {
	return fmt.Sprintf("%s is not safe to run as the service (%s)", e.Path, e.Reason)
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

// EnableReport is what Enable did besides the service: Stopped is the mode of
// the run it stopped so that the service could take its place, "" if none
// ran, and PID that run's supervisor.
type EnableReport struct {
	Stopped string
	PID     int
}

// TerminateRun sends SIGTERM to the supervisor of a run, which then stops and
// removes its rules. Tests replace it so that no real process is signalled.
var TerminateRun = func(pid int) error { return syscall.Kill(pid, syscall.SIGTERM) }

var enableWait = 10 * time.Second

// manager is the service manager of this machine's init, for files under P.
func (o *Ops) manager() (initsys.Manager, error) { return o.managerFor(o.P.BinPath) }

// managerFor is manager for a service that runs bin.
func (o *Ops) managerFor(bin string) (initsys.Manager, error) {
	return initsys.For(sysinfo.DetectInit(o.Root), o.Exec, bin, o.P)
}

// Enable installs, enables and starts the service, waits for it to run and
// saves run mode boot. Once nothing else stands in the way, a background or
// foreground run is stopped first and the service takes its place.
func (o *Ops) Enable(ctx context.Context) (EnableReport, error) {
	var r EnableReport
	cfg, err := config.Load(o.P)
	if err != nil {
		return r, err
	}
	if cfg.Strategy == nil {
		return r, render.ErrNoStrategy
	}
	// Without a service on this init, the binary does not matter.
	if _, err := o.manager(); err != nil {
		return r, err
	}
	bin, err := o.serviceBinary()
	if err != nil {
		return r, err
	}
	m, err := o.managerFor(bin)
	if err != nil {
		return r, err
	}
	if err := o.refuseConflicts(ctx); err != nil {
		return r, err
	}
	if pid, mode, held := supervisor.LockInfo(o.P); held && mode != "service" {
		if err := o.stopForService(pid, mode); err != nil {
			return r, err
		}
		r.Stopped, r.PID = mode, pid
	}
	if err := m.Install(); err != nil {
		return r, err
	}
	if err := m.Enable(ctx); err != nil {
		return r, err
	}
	if err := m.Start(ctx); err != nil {
		return r, err
	}
	if _, ok := o.waitRunning("service", nil); !ok {
		return r, ErrServiceNotStarted
	}
	return r, o.SetRunMode(config.RunBoot)
}

// stopForService stops the run in mode whose supervisor is pid, so that the
// service can take its place, and waits until it has exited: SIGTERM, then
// until no live process holds the lock. It stops only a background or
// foreground run, and never one of this process (the TUI's own run), which
// only its owner can stop.
func (o *Ops) stopForService(pid int, mode string) error {
	if mode != "background" && mode != "foreground" || pid == os.Getpid() {
		return supervisor.ErrAlreadyRunning{Mode: mode}
	}
	if err := TerminateRun(pid); err != nil {
		return StopRunError{Mode: mode, PID: pid, Err: err}
	}
	for range 100 {
		if _, _, held := supervisor.LockInfo(o.P); !held {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return StopRunError{Mode: mode, PID: pid, Err: ErrStopTimeout}
}

// serviceBinary is the splitdpi the service runs: BinPath, where install.sh
// puts it by default, as always; else this binary, if only root can change
// it. /proc/self/exe is what os.Executable reads on Linux, the path with
// every symlink resolved; like the rest of the host it is read through Root.
func (o *Ops) serviceBinary() (string, error) {
	if _, err := os.Stat(o.P.BinPath); err == nil {
		return o.P.BinPath, nil
	}
	self, err := fs.ReadLink(o.Root, "proc/self/exe")
	if err != nil {
		return "", BinaryMissingError{Path: o.P.BinPath}
	}
	// A binary replaced while it runs reads as "PATH (deleted)": PATH is the new one.
	self = filepath.Clean(strings.TrimSuffix(self, " (deleted)"))
	reason, err := unsafeReason(o.Root, self)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "", BinaryMissingError{Path: o.P.BinPath}
	case err != nil:
		return "", err
	case reason != "":
		return "", UnsafeBinaryError{Path: self, Reason: reason, Install: o.P.BinPath}
	}
	return self, nil
}

// unsafeDirs keep temporary files or a user's files: home directories,
// including where Fedora's atomic desktops keep them.
var unsafeDirs = []string{"/tmp", "/home", "/var/home", "/root", "/var/roothome"}

// unsafeReason says why root must not run bin, a host path read through
// root, at every boot; "" when only root can change it: it and its directory
// are root's, no group or others may write to them, and it is not in a
// temporary or home directory.
func unsafeReason(root fs.FS, bin string) (string, error) {
	for _, d := range unsafeDirs {
		if strings.HasPrefix(bin, d+"/") {
			return UnsafeLocation, nil
		}
	}
	for _, p := range []string{bin, filepath.Dir(bin)} {
		// Joined to ".", the host path is a path in root; "/" becomes ".".
		fi, err := fs.Stat(root, filepath.Join(".", p))
		if err != nil {
			return "", err
		}
		if st, ok := fi.Sys().(*syscall.Stat_t); !ok || st.Uid != 0 {
			return UnsafeOwner, nil
		}
		if fi.Mode().Perm()&0o022 != 0 {
			return UnsafeWritable, nil
		}
	}
	return "", nil
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
