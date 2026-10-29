package cli

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"testing/fstest"

	"github.com/KadirBerkpolat1/SplitDPI/internal/engine"
	"github.com/KadirBerkpolat1/SplitDPI/internal/ops"
	"github.com/KadirBerkpolat1/SplitDPI/internal/paths"
)

// No test here forks splitdpi: Detach is faked, and "stop" signals a sleep
// this test starts.

func installEngine(t *testing.T, p paths.Paths) {
	t.Helper()
	dir := filepath.Join(p.EngineDir, "zapret2-"+engine.Version)
	bin := filepath.Join(dir, "binaries", "linux-x86_64")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string]string{
		filepath.Join(bin, "nfqws2"):          "",
		filepath.Join(p.EngineDir, "current"): "zapret2-" + engine.Version + "\nlinux-x86_64\n",
	} {
		if err := os.WriteFile(name, []byte(data), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

// backgroundOps is ready to run, with a Detach that fails the test when called.
func backgroundOps(t *testing.T, root fstest.MapFS) *ops.Ops {
	o, _ := runOpsFor(t, root)
	writeConfig(t, o.P, withStrategy)
	installEngine(t, o.P)
	o.Detach = func(*exec.Cmd) (<-chan error, error) {
		t.Error("detached process started")
		return nil, errors.New("unexpected")
	}
	return o
}

func TestRunBackground(t *testing.T) {
	o := backgroundOps(t, fstest.MapFS{})
	os.MkdirAll(filepath.Dir(o.P.LogFile), 0o755)
	os.WriteFile(o.P.LogFile, []byte("earlier\n"), 0o644)
	var c *exec.Cmd
	o.Detach = func(cmd *exec.Cmd) (<-chan error, error) {
		c = cmd
		cmd.Stderr.(*os.File).WriteString("detached\n")
		holdLock(t, o.P, "background") // the detached run is up
		return make(chan error), nil
	}
	out, code := runOps(t, o, "run", "--background")
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	has(t, out, "running in the background (pid 999999999)")
	has(t, out, "sudo splitdpi stop")

	self, _ := os.Executable()
	if c.Path != self || !slices.Equal(c.Args[1:], []string{"run", ops.DetachedArg}) {
		t.Errorf("command %s %q, want %s run %s", c.Path, c.Args, self, ops.DetachedArg)
	}
	if c.SysProcAttr == nil || !c.SysProcAttr.Setsid {
		t.Error("not in a session of its own")
	}
	if c.Dir != "/" {
		t.Errorf("dir %q", c.Dir)
	}
	name := func(v any) string {
		if f, ok := v.(*os.File); ok {
			return f.Name()
		}
		return ""
	}
	if name(c.Stdin) != os.DevNull || name(c.Stdout) != o.P.LogFile || name(c.Stderr) != o.P.LogFile {
		t.Errorf("stdio %q %q %q", name(c.Stdin), name(c.Stdout), name(c.Stderr))
	}
	if b, _ := os.ReadFile(o.P.LogFile); string(b) != "earlier\ndetached\n" {
		t.Errorf("log %q, want the output appended", b)
	}
}

func TestRunBackgroundExitsEarly(t *testing.T) {
	o := backgroundOps(t, fstest.MapFS{})
	o.Detach = func(*exec.Cmd) (<-chan error, error) {
		exited := make(chan error, 1)
		exited <- errors.New("exit status 1")
		return exited, nil
	}
	out, code := runOps(t, o, "run", "--background")
	eq(t, code, 1)
	has(t, out, "did not start in the background")
	has(t, out, o.P.LogFile)
}

func TestRunBackgroundRefuses(t *testing.T) {
	t.Run("already running", func(t *testing.T) {
		o := backgroundOps(t, fstest.MapFS{})
		holdLock(t, o.P, "foreground")
		out, code := runOps(t, o, "run", "--background")
		eq(t, code, 1)
		has(t, out, "already running (foreground)")
	})
	t.Run("conflict", func(t *testing.T) {
		o := backgroundOps(t, fstest.MapFS{
			"proc/1156/stat": {Data: []byte("1156 (nfqws2) S 1 1156 1156 0 -1 4194560 0 0 0 0")},
		})
		out, code := runOps(t, o, "run", "--background")
		eq(t, code, 1)
		has(t, out, "nfqws2")
	})
	t.Run("no strategy", func(t *testing.T) {
		o := backgroundOps(t, fstest.MapFS{})
		writeConfig(t, o.P, "")
		out, code := runOps(t, o, "run", "--background")
		eq(t, code, 1)
		has(t, out, "splitdpi scan")
	})
	t.Run("no engine", func(t *testing.T) {
		o := backgroundOps(t, fstest.MapFS{})
		os.RemoveAll(o.P.EngineDir)
		_, code := runOps(t, o, "run", "--background")
		eq(t, code, 1)
	})
}

// A background run blocks the others like a foreground one, and says what it is.
func TestBackgroundBlocksRunAndEnable(t *testing.T) {
	o := backgroundOps(t, systemdRoot())
	installBinary(t, o.P)
	holdLock(t, o.P, "background")
	for _, cmd := range []string{"run", "enable"} {
		out, code := runOps(t, o, cmd)
		eq(t, code, 1)
		has(t, out, "already running (background (stops at reboot))")
		has(t, out, "splitdpi stop")
	}
	o2, f := runOpsFor(t, systemdRoot())
	writeConfig(t, o2.P, withStrategy)
	installBinary(t, o2.P)
	holdLock(t, o2.P, "background")
	runOps(t, o2, "enable")
	if slices.ContainsFunc(f.calls, func(c string) bool { return strings.HasPrefix(c, "systemctl") }) {
		t.Errorf("service touched while a background run holds the lock: %q", f.calls)
	}
}

func TestStatusBackground(t *testing.T) {
	o, _ := runOpsFor(t, fstest.MapFS{})
	holdLock(t, o.P, "background")
	out, code := runOps(t, o, "status")
	eq(t, code, 0)
	has(t, out, "State: running")
	has(t, out, "Mode: background (stops at reboot), pid 999999999")
}

// sleeper stands in for a background supervisor: it holds the lock under the
// PID of a sleep that only the expected SIGTERM ends.
// The returned channel delivers how the sleep ended, before the lock is released.
func sleeper(t *testing.T, p paths.Paths) <-chan *os.ProcessState {
	t.Helper()
	c := exec.Command("sleep", "30")
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Process.Kill() })
	release := holdLockAs(t, p, "background", c.Process.Pid)
	ended := make(chan *os.ProcessState, 1)
	go func() {
		c.Wait()
		ended <- c.ProcessState
		release()
	}()
	return ended
}

func terminated(t *testing.T, ended <-chan *os.ProcessState) {
	t.Helper()
	select {
	case ps := <-ended:
		if ws, ok := ps.Sys().(syscall.WaitStatus); !ok || !ws.Signaled() || ws.Signal() != syscall.SIGTERM {
			t.Errorf("background run ended with %v, want SIGTERM", ps)
		}
	default:
		t.Error("background run still there")
	}
}

func TestStopBackground(t *testing.T) {
	o, f := runOpsFor(t, systemdRoot())
	ended := sleeper(t, o.P)
	out, code := runOps(t, o, "stop")
	if code != 0 {
		t.Fatalf("stop exit %d:\n%s", code, out)
	}
	has(t, out, "Stopped")
	terminated(t, ended)
	if slices.ContainsFunc(f.calls, func(c string) bool { return strings.HasPrefix(c, "systemctl") }) {
		t.Errorf("a background run was stopped through the service: %q", f.calls)
	}
}

func TestRevertStopsBackground(t *testing.T) {
	o, _ := runOpsFor(t, fstest.MapFS{})
	ended := sleeper(t, o.P)
	out, code := runOps(t, o, "revert")
	eq(t, code, 0)
	has(t, out, "clean")
	terminated(t, ended)
}
