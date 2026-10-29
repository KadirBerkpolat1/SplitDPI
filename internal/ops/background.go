package ops

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"github.com/KadirBerkpolat1/SplitDPI/internal/config"
	"github.com/KadirBerkpolat1/SplitDPI/internal/supervisor"
)

// DetachedArg is the hidden `run` flag of the process `run --background` starts.
const DetachedArg = "--detached"

// BackgroundNotStartedError means the detached run exited or did not report
// running within 10 s; its output is in Log.
type BackgroundNotStartedError struct{ Log string }

func (e BackgroundNotStartedError) Error() string {
	return "background run did not start; see " + e.Log
}

// Background is `splitdpi run --background`: it refuses like Run, then starts
// `run` again in a session of its own (no terminal, so closing one does not
// stop it; nothing starts it at boot) and waits until it runs. It returns the
// supervisor's PID.
func (o *Ops) Background(ctx context.Context) (int, error) {
	if err := o.refuseConflicts(ctx); err != nil {
		return 0, err
	}
	cfg, err := config.Load(o.P)
	if err != nil {
		return 0, err
	}
	// The detached process reports its errors only to the log: catch them here.
	if _, err := o.prepare(cfg, "background", io.Discard, nil); err != nil {
		return 0, err
	}
	if _, mode, held := supervisor.LockInfo(o.P); held {
		return 0, supervisor.ErrAlreadyRunning{Mode: mode}
	}
	self, err := os.Executable()
	if err != nil {
		return 0, err
	}
	null, err := os.Open(os.DevNull)
	if err != nil {
		return 0, err
	}
	defer null.Close()
	if err := os.MkdirAll(filepath.Dir(o.P.LogFile), 0o755); err != nil {
		return 0, err
	}
	logf, err := os.OpenFile(o.P.LogFile, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return 0, err
	}
	defer logf.Close()

	c := exec.Command(self, "run", DetachedArg)
	c.Dir = "/"
	c.Stdin, c.Stdout, c.Stderr = null, logf, logf
	c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	detach := o.Detach
	if detach == nil {
		detach = startDetached
	}
	exited, err := detach(c)
	if err != nil {
		return 0, err
	}
	pid, ok := o.waitRunning("background", exited)
	if !ok {
		return 0, BackgroundNotStartedError{Log: o.P.LogFile}
	}
	return pid, nil
}

// RunDetached is the process Background starts: Run in mode "background".
func (o *Ops) RunDetached(ctx context.Context) error { return o.run(ctx, "background") }

func startDetached(c *exec.Cmd) (<-chan error, error) {
	if err := c.Start(); err != nil {
		return nil, err
	}
	exited := make(chan error, 1)
	go func() { exited <- c.Wait() }()
	return exited, nil
}
