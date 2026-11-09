package ops

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/KadirBerkpolat1/SplitDPI/internal/config"
	"github.com/KadirBerkpolat1/SplitDPI/internal/supervisor"
	"github.com/KadirBerkpolat1/SplitDPI/internal/sysinfo"
)

// DetachedArg is the hidden `run` flag of the process `run --background` starts.
const DetachedArg = "--detached"

// backgroundUnit is the transient systemd unit of a background run. Unlike
// splitdpi.service, nothing enables it.
const backgroundUnit = "splitdpi-background.service"

// unitPoll is how often Background asks systemd whether backgroundUnit still runs.
const unitPoll = 250 * time.Millisecond

// environ is the environment backgroundUnit takes its variables from.
var environ = os.Environ

// BackgroundNotStartedError means the background run exited or did not report
// running within 10 s; its output is in Log.
type BackgroundNotStartedError struct{ Log string }

func (e BackgroundNotStartedError) Error() string {
	return "background run did not start; see " + e.Log
}

// Background is `splitdpi run --background`: it refuses like Run, then starts
// `run` again away from this terminal, so closing it does not stop the run,
// and waits until it runs. On systemd the run is a transient system unit:
// logging out leaves it alone and shutdown stops it with SIGTERM. Elsewhere,
// or when systemd-run fails, it is a child in a session of its own. Nothing
// starts it at boot. It returns the supervisor's PID.
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
	if err := os.MkdirAll(filepath.Dir(o.P.LogFile), 0o755); err != nil {
		return 0, err
	}
	logf, err := os.OpenFile(o.P.LogFile, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return 0, err
	}
	defer logf.Close()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var exited <-chan error
	if o.haveSystemdRun() {
		if exited, err = o.startUnit(ctx, self); err != nil {
			fmt.Fprintln(logf, err)
		}
	}
	if exited == nil {
		if exited, err = o.spawnDetached(self, logf); err != nil {
			return 0, err
		}
	}
	pid, ok := o.waitRunning("background", exited)
	if !ok {
		return 0, BackgroundNotStartedError{Log: o.P.LogFile}
	}
	return pid, nil
}

// RunDetached is the process Background starts: Run in mode "background".
func (o *Ops) RunDetached(ctx context.Context) error { return o.run(ctx, "background") }

// haveSystemdRun reports whether a background run can be a transient systemd unit.
func (o *Ops) haveSystemdRun() bool {
	if sysinfo.DetectInit(o.Root) != sysinfo.Systemd {
		return false
	}
	_, err := o.lookPath("systemd-run")
	return err == nil
}

// startUnit runs self as backgroundUnit and returns a channel that delivers
// once the unit has ended.
func (o *Ops) startUnit(ctx context.Context, self string) (<-chan error, error) {
	// systemd-run refuses the name while a failed unit holds it. --collect
	// keeps ours from staying failed, not one started some other way.
	if state, _ := o.unitState(ctx); state == "failed" {
		if _, err := o.Exec(ctx, "", "systemctl", "reset-failed", backgroundUnit); err != nil {
			return nil, err
		}
	}
	if _, err := o.Exec(ctx, "", "systemd-run", unitArgs(o.P.LogFile, self)...); err != nil {
		return nil, err
	}
	return o.unitEnded(ctx), nil
}

// unitArgs is the systemd-run command line of a background run writing to log.
// The run is several processes: KillMode=control-group gives each the SIGTERM,
// so the guard drops the DNS redirect even when the supervisor has died.
// TimeoutStopSec outlasts the supervisor's 5 s wait for the guard plus its
// table cleanup before SIGKILL.
func unitArgs(log, self string) []string {
	args := []string{
		"--unit=" + backgroundUnit,
		"--description=SplitDPI background run",
		"--service-type=exec",
		"--collect",
		"--property=Restart=no",
		"--property=KillMode=control-group",
		"--property=KillSignal=SIGTERM",
		"--property=TimeoutStopSec=15s",
		"--property=StandardOutput=append:" + log,
		"--property=StandardError=append:" + log,
	}
	for _, kv := range environ() {
		name, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(name, "SPLITDPI_") || name == "LANG" || strings.HasPrefix(name, "LC_") {
			args = append(args, "--setenv="+kv)
		}
	}
	return append(args, "--", self, "run", DetachedArg)
}

// unitState is the ActiveState of backgroundUnit, "inactive" when there is none.
func (o *Ops) unitState(ctx context.Context) (string, error) {
	out, err := o.Exec(ctx, "", "systemctl", "show", "--property=ActiveState", "--value", backgroundUnit)
	return strings.TrimSpace(string(out)), err
}

// unitEnded delivers once backgroundUnit is no longer active, asking systemd
// every unitPoll until ctx is done.
func (o *Ops) unitEnded(ctx context.Context) <-chan error {
	ended := make(chan error, 1)
	go func() {
		tick := time.NewTicker(unitPoll)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
			}
			if state, err := o.unitState(ctx); err == nil && state != "active" {
				ended <- fmt.Errorf("%s is %s", backgroundUnit, state)
				return
			}
		}
	}()
	return ended
}

// spawnDetached starts self as `run --detached` in a session of its own,
// writing to logf.
func (o *Ops) spawnDetached(self string, logf *os.File) (<-chan error, error) {
	null, err := os.Open(os.DevNull)
	if err != nil {
		return nil, err
	}
	defer null.Close()
	c := exec.Command(self, "run", DetachedArg)
	c.Dir = "/"
	c.Stdin, c.Stdout, c.Stderr = null, logf, logf
	c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	detach := o.Detach
	if detach == nil {
		detach = startDetached
	}
	return detach(c)
}

func startDetached(c *exec.Cmd) (<-chan error, error) {
	if err := c.Start(); err != nil {
		return nil, err
	}
	exited := make(chan error, 1)
	go func() { exited <- c.Wait() }()
	return exited, nil
}
