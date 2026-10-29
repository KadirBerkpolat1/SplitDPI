package ops

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
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

var enableWait = 10 * time.Second

// manager is the service manager of this machine's init, for files under P.
func (o *Ops) manager() (initsys.Manager, error) {
	return initsys.For(sysinfo.DetectInit(o.Root), o.Exec, o.P.BinPath, o.P)
}

// Enable installs, enables and starts the service, then waits for it to run.
func (o *Ops) Enable(ctx context.Context) error {
	cfg, err := config.Load(o.P)
	if err != nil {
		return err
	}
	if cfg.Strategy == nil {
		return render.ErrNoStrategy
	}
	m, err := o.manager()
	if err != nil {
		return err
	}
	if _, err := os.Stat(o.P.BinPath); err != nil {
		return BinaryMissingError{Path: o.P.BinPath}
	}
	if err := o.refuseConflicts(ctx); err != nil {
		return err
	}
	if _, mode, held := supervisor.LockInfo(o.P); held && mode != "service" {
		return supervisor.ErrAlreadyRunning{Mode: mode}
	}
	if err := m.Install(); err != nil {
		return err
	}
	if err := m.Enable(ctx); err != nil {
		return err
	}
	if err := m.Start(ctx); err != nil {
		return err
	}
	if _, ok := o.waitRunning("service", nil); !ok {
		return ErrServiceNotStarted
	}
	return nil
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

// Disable stops, disables and removes the service.
func (o *Ops) Disable(ctx context.Context) error {
	m, err := o.manager()
	if err != nil {
		return err
	}
	if !m.Installed() {
		return ErrNotEnabled
	}
	return removeService(ctx, m)
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
// (after LogFile.1). follow keeps writing new lines until ctx is done.
func (o *Ops) Logs(ctx context.Context, follow bool, w io.Writer) error {
	if m, err := o.manager(); err == nil {
		if cmd := m.LogsCmd(follow); cmd != nil {
			if !follow {
				out, err := o.Exec(ctx, "", cmd[0], cmd[1:]...)
				w.Write(out)
				return err
			}
			c := exec.CommandContext(ctx, cmd[0], cmd[1:]...)
			c.Stdout, c.Stderr = w, w
			return c.Run()
		}
	}
	found := false
	for _, f := range []string{o.P.LogFile + ".1", o.P.LogFile} {
		if b, err := os.ReadFile(f); err == nil {
			w.Write(b)
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
		f.Close()
	}
}

func serviceLabel(o *Ops) string {
	return fmt.Sprintf("%s service (%s)", o.P.NftTable, sysinfo.DetectInit(o.Root))
}
