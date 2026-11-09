package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"testing/fstest"

	"github.com/KadirBerkpolat1/SplitDPI/internal/config"
	"github.com/KadirBerkpolat1/SplitDPI/internal/ops"
	"github.com/KadirBerkpolat1/SplitDPI/internal/paths"
	"github.com/KadirBerkpolat1/SplitDPI/internal/supervisor"
)

// Fake runners, a temp root and a replaced ops.TerminateRun: no real service,
// table, process or signal is touched.

func systemctlCalls(f *fakeNft) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.DeleteFunc(slices.Clone(f.calls), func(c string) bool { return !strings.HasPrefix(c, "systemctl ") })
}

func savedRunMode(t *testing.T, p paths.Paths) config.RunMode {
	t.Helper()
	c, err := config.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	return c.RunMode
}

func enableUnit(t *testing.T, p paths.Paths) {
	t.Helper()
	for _, f := range []string{p.SystemdUnit, p.SystemdLink} {
		if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(p.SystemdUnit, []byte("[Unit]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(p.SystemdUnit, p.SystemdLink); err != nil {
		t.Fatal(err)
	}
}

// Setting run mode boot only saves it and warns: no service is touched.
func TestConfigSetRunModeBootOnlyWarns(t *testing.T) {
	o, f := runOpsFor(t, systemdRoot())
	writeConfig(t, o.P, withStrategy)
	installBinary(t, o)
	out, code := runOps(t, o, "config", "set", "run_mode", "boot")
	eq(t, code, 0)
	has(t, out, "run_mode is now boot.")
	has(t, out, "the service is not set up. To set it up: sudo splitdpi enable")
	if len(f.calls) > 0 {
		t.Errorf("external calls %q", f.calls)
	}
	if _, err := os.Stat(o.P.SystemdUnit); !os.IsNotExist(err) {
		t.Errorf("unit written: %v", err)
	}

	enableUnit(t, o.P)
	out, _ = runOps(t, o, "config", "set", "run_mode", "window")
	has(t, out, "The service still starts SplitDPI at boot. To stop that: sudo splitdpi disable")
	if _, err := os.Lstat(o.P.SystemdLink); err != nil || len(f.calls) > 0 {
		t.Errorf("service changed: %v, calls %q", err, f.calls)
	}
}

// terminateWith replaces ops.TerminateRun: it records the signal among f's
// calls, then does what to the run.
func terminateWith(t *testing.T, f *fakeNft, what func() error) {
	t.Helper()
	old := ops.TerminateRun
	ops.TerminateRun = func(pid int) error {
		f.record(fmt.Sprintf("SIGTERM %d", pid))
		return what()
	}
	t.Cleanup(func() { ops.TerminateRun = old })
}

// steps is what enable did to the service and the run, in order.
func steps(f *fakeNft) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.DeleteFunc(slices.Clone(f.calls), func(c string) bool {
		return !strings.HasPrefix(c, "systemctl ") && !strings.HasPrefix(c, "SIGTERM ")
	})
}

// enable sets the service up first, with the background run still on; then
// it stops that run and waits until it has exited, starts the service in its
// place and saves run mode boot.
func TestEnableTakesOverBackgroundRun(t *testing.T) {
	o, f := runOpsFor(t, systemdRoot())
	writeConfig(t, o.P, withStrategy)
	installBinary(t, o)
	installEngine(t, o.P)
	release := holdLock(t, o.P, "background")
	terminateWith(t, f, func() error {
		if want := []string{"systemctl daemon-reload", "systemctl enable splitdpi"}; !slices.Equal(systemctlCalls(f), want) {
			t.Errorf("at the stop: calls %q, want %q", systemctlCalls(f), want)
		}
		release()
		return nil
	})
	f.hooks = map[string]func(){"systemctl start splitdpi": func() { holdLock(t, o.P, "service") }}
	out, code := runOps(t, o, "enable")
	if code != 0 {
		t.Fatalf("enable exit %d:\n%s", code, out)
	}
	has(t, out, "Stopped the background run (pid 999999999): the service runs the bypass instead.")
	has(t, out, "Service enabled and running.")
	if want := []string{"systemctl daemon-reload", "systemctl enable splitdpi", "SIGTERM 999999999", "systemctl start splitdpi"}; !slices.Equal(steps(f), want) {
		t.Errorf("steps %q, want %q", steps(f), want)
	}
	if got := savedRunMode(t, o.P); got != config.RunBoot {
		t.Errorf("run mode %q, want boot", got)
	}
}

// A run that cannot be stopped keeps running: the service is removed again
// and the run mode stays.
func TestEnableRunWontStop(t *testing.T) {
	o, f := runOpsFor(t, systemdRoot())
	writeConfig(t, o.P, withStrategy)
	installBinary(t, o)
	installEngine(t, o.P)
	holdLock(t, o.P, "background")
	terminateWith(t, f, func() error { return syscall.ESRCH })
	out, code := runOps(t, o, "enable")
	eq(t, code, 1)
	has(t, out, "Could not stop the background run (pid 999999999)")
	has(t, out, "The bypass still runs (background).")
	if strings.Contains(out, "Stopped the") {
		t.Errorf("reports a takeover:\n%s", out)
	}
	if want := []string{"systemctl daemon-reload", "systemctl enable splitdpi", "SIGTERM 999999999", "systemctl disable splitdpi"}; !slices.Equal(steps(f), want) {
		t.Errorf("steps %q, want %q", steps(f), want)
	}
	if _, err := os.Stat(o.P.SystemdUnit); !os.IsNotExist(err) {
		t.Errorf("unit left: %v", err)
	}
	if got := savedRunMode(t, o.P); got != config.RunWindow {
		t.Errorf("run mode %q, want window", got)
	}
}

// A check that fails stops nothing: the background run stays and the
// service is not touched.
func TestEnableCheckFailsStopsNothing(t *testing.T) {
	o, f := runOpsFor(t, systemdRoot())
	writeConfig(t, o.P, withStrategy)
	installBinary(t, o)
	o.Root.(fstest.MapFS)["usr/local"].Sys = &syscall.Stat_t{Uid: 1000}
	holdLock(t, o.P, "background")
	terminateWith(t, f, func() error { return nil })
	out, code := runOps(t, o, "enable")
	eq(t, code, 1)
	has(t, out, "The service cannot run "+o.P.BinPath+": /usr/local does not belong to root.")
	if len(steps(f)) > 0 || strings.Contains(out, "Stopped the") {
		t.Errorf("steps %q, output:\n%s", steps(f), out)
	}
	if _, mode, held := supervisor.LockInfo(o.P); !held || mode != "background" {
		t.Errorf("the background run is gone: %q %v", mode, held)
	}
	if got := savedRunMode(t, o.P); got != config.RunWindow {
		t.Errorf("run mode %q, want window", got)
	}
}

// startFailsOps can enable the service, but the service does not start
// after the background run there was stopped; a background run started
// again comes up with pid 4545, or exits at once with fails.
func startFailsOps(t *testing.T, fails bool) (*ops.Ops, *fakeNft) {
	t.Helper()
	o, f := runOpsFor(t, systemdRoot())
	writeConfig(t, o.P, withStrategy)
	installBinary(t, o)
	installEngine(t, o.P)
	release := holdLock(t, o.P, "background")
	terminateWith(t, f, func() error { release(); return nil })
	f.errs = map[string]error{"systemctl start splitdpi": errors.New("systemctl: exit status 1")}
	// No systemd-run: the run starts again through Detach.
	o.LookPath = func(name string) (string, error) {
		if name == "systemd-run" {
			return "", exec.ErrNotFound
		}
		return "/usr/bin/" + name, nil
	}
	o.Detach = func(*exec.Cmd) (<-chan error, error) {
		exited := make(chan error, 1)
		if fails {
			exited <- errors.New("exit status 1")
		} else {
			holdLockAs(t, o.P, "background", 4545)
		}
		return exited, nil
	}
	return o, f
}

// A service that does not start after the run was stopped is removed again,
// the run starts again in the background, and enable says so.
func TestEnableStartFailsRestartsBackground(t *testing.T) {
	o, f := startFailsOps(t, false)
	out, code := runOps(t, o, "enable")
	eq(t, code, 1)
	has(t, out, "Could not start the service: systemctl: exit status 1.\nThe previous run was restarted in the background (pid 4545).")
	if strings.Contains(out, "Stopped the") || strings.Contains(out, "Service enabled") {
		t.Errorf("reports a takeover:\n%s", out)
	}
	want := []string{"systemctl daemon-reload", "systemctl enable splitdpi", "SIGTERM 999999999", "systemctl start splitdpi", "systemctl stop splitdpi", "systemctl disable splitdpi"}
	if !slices.Equal(steps(f), want) {
		t.Errorf("steps %q, want %q", steps(f), want)
	}
	if _, err := os.Stat(o.P.SystemdUnit); !os.IsNotExist(err) {
		t.Errorf("unit left: %v", err)
	}
	if got := savedRunMode(t, o.P); got != config.RunWindow {
		t.Errorf("run mode %q, want window", got)
	}
}

// When the run cannot start again either, enable says that the bypass is
// off and how to start it.
func TestEnableStartFailsBypassDown(t *testing.T) {
	o, _ := startFailsOps(t, true)
	out, code := runOps(t, o, "enable")
	eq(t, code, 1)
	has(t, out, "Could not start the service: systemctl: exit status 1.")
	has(t, out, "did not start in the background")
	has(t, out, "The bypass is not running now. Start it with: sudo splitdpi run --background")
}

// A config that cannot be read has no run mode: status says so instead of
// telling a user with the service enabled to disable it.
func TestStatusUnreadableConfig(t *testing.T) {
	o, _ := runOpsFor(t, systemdRoot())
	writeConfig(t, o.P, "lang = \n")
	enableUnit(t, o.P)
	out, code := runOps(t, o, "status")
	eq(t, code, 1)
	has(t, out, "Starts at boot: yes.")
	has(t, out, "The run mode cannot be read: config.toml: line 1")
	if strings.Contains(out, "sudo splitdpi disable") || strings.Contains(out, "sudo splitdpi enable") {
		t.Errorf("run mode advice without a run mode:\n%s", out)
	}
}

// The unit enable writes keeps the service from restarting after a crash
// loop (3) or when another splitdpi already runs (4).
func TestEnableWritesUnit(t *testing.T) {
	o, f := runOpsFor(t, systemdRoot())
	writeConfig(t, o.P, withStrategy)
	installBinary(t, o)
	installEngine(t, o.P)
	f.hooks = map[string]func(){"systemctl start splitdpi": func() { holdLock(t, o.P, "service") }}
	if out, code := runOps(t, o, "enable"); code != 0 {
		t.Fatalf("enable exit %d:\n%s", code, out)
	}
	unit, _ := os.ReadFile(o.P.SystemdUnit)
	for _, want := range []string{"ExecStart=" + o.P.BinPath + " run --service\n", "ExecStopPost=" + o.P.BinPath + " revert --rules-only\n", "RestartPreventExitStatus=3 4\n"} {
		has(t, string(unit), want)
	}
}

func TestStatusBootLine(t *testing.T) {
	o, _ := runOpsFor(t, systemdRoot())
	writeConfig(t, o.P, withStrategy)
	holdLock(t, o.P, "background")
	out, code := runOps(t, o, "status")
	eq(t, code, 0)
	has(t, out, "Starts at boot: no.")

	writeConfig(t, o.P, "run_mode = \"boot\"\n"+withStrategy)
	out, _ = runOps(t, o, "status")
	has(t, out, "the service is not set up. To set it up: sudo splitdpi enable")

	enableUnit(t, o.P)
	out, _ = runOps(t, o, "status")
	has(t, out, "Starts at boot: yes.")
	if strings.Contains(out, "not set up") {
		t.Errorf("warns with the service enabled:\n%s", out)
	}
}

// A service that finds another run there exits 4, which the unit does not
// restart; its stop's revert --rules-only leaves that run's table alone.
func TestServiceFindsAnotherRun(t *testing.T) {
	o, f := runOpsFor(t, systemdRoot())
	writeConfig(t, o.P, withStrategy)
	installEngine(t, o.P)
	release := holdLock(t, o.P, "background")
	out, code := runOps(t, o, "run", "--service")
	eq(t, code, 4)
	has(t, out, "already running (background)")

	f.table = true
	out, code = runOps(t, o, "revert", "--rules-only")
	eq(t, code, 0)
	has(t, out, "Kept the firewall rules: they belong to the splitdpi running now (background).")
	if f.saw("nft delete table inet splitdpi") {
		t.Error("the running background run's table was deleted")
	}

	release()
	if _, code := runOps(t, o, "revert", "--rules-only"); code != 0 || !f.saw("nft delete table inet splitdpi") {
		t.Errorf("with no run left: code %d, calls %q", code, f.calls)
	}
}

// disable, revert and uninstall leave run mode boot: nothing starts
// SplitDPI at boot afterwards.
func TestLeavingTheServiceLeavesBootMode(t *testing.T) {
	for _, tc := range []struct {
		name    string
		args    []string
		service bool
	}{
		{"disable", []string{"disable"}, true},
		{"disable, not enabled", []string{"disable"}, false},
		{"revert", []string{"revert"}, true},
		{"uninstall", []string{"uninstall", "--yes"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o, _ := runOpsFor(t, systemdRoot())
			writeConfig(t, o.P, "run_mode = \"boot\"\n"+withStrategy)
			if tc.service {
				enableUnit(t, o.P)
			}
			if out, code := runOps(t, o, tc.args...); code != 0 {
				t.Fatalf("exit %d:\n%s", code, out)
			}
			if got := savedRunMode(t, o.P); got != config.RunWindow {
				t.Errorf("run mode %q, want window", got)
			}
		})
	}
}
