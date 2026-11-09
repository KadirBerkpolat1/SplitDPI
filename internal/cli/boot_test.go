package cli

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/KadirBerkpolat1/SplitDPI/internal/config"
	"github.com/KadirBerkpolat1/SplitDPI/internal/ops"
	"github.com/KadirBerkpolat1/SplitDPI/internal/paths"
)

// Fake runners, a temp root and a replaced ops.TerminateRun: no real service,
// table or process is touched.

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
	installBinary(t, o.P)
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

// enable stops a background run, waits until it has exited, then sets the
// service up in its place and saves run mode boot.
func TestEnableTakesOverBackgroundRun(t *testing.T) {
	o, f := runOpsFor(t, systemdRoot())
	writeConfig(t, o.P, withStrategy)
	installBinary(t, o.P)
	release := holdLock(t, o.P, "background")
	var signalled []int
	old := ops.TerminateRun
	ops.TerminateRun = func(pid int) error {
		if len(systemctlCalls(f)) > 0 {
			t.Error("service touched before the run was stopped")
		}
		signalled = append(signalled, pid)
		release()
		return nil
	}
	t.Cleanup(func() { ops.TerminateRun = old })
	f.hooks = map[string]func(){"systemctl start splitdpi": func() { holdLock(t, o.P, "service") }}
	out, code := runOps(t, o, "enable")
	if code != 0 {
		t.Fatalf("enable exit %d:\n%s", code, out)
	}
	has(t, out, "Stopped the background run (pid 999999999): the service runs the bypass instead.")
	has(t, out, "Service enabled and running.")
	if !slices.Equal(signalled, []int{999999999}) {
		t.Errorf("signalled %v", signalled)
	}
	if want := []string{"systemctl daemon-reload", "systemctl enable splitdpi", "systemctl start splitdpi"}; !slices.Equal(systemctlCalls(f), want) {
		t.Errorf("calls %q, want %q", systemctlCalls(f), want)
	}
	if got := savedRunMode(t, o.P); got != config.RunBoot {
		t.Errorf("run mode %q, want boot", got)
	}
}

// A run that cannot be stopped is reported, and the service is left alone.
func TestEnableRunWontStop(t *testing.T) {
	o, f := runOpsFor(t, systemdRoot())
	writeConfig(t, o.P, withStrategy)
	installBinary(t, o.P)
	holdLock(t, o.P, "background")
	out, code := runOps(t, o, "enable") // the real TerminateRun: pid 999999999 does not exist
	eq(t, code, 1)
	has(t, out, "Could not stop the background run (pid 999999999)")
	if len(systemctlCalls(f)) > 0 {
		t.Errorf("service touched: %q", f.calls)
	}
}

// The unit enable writes keeps the service from restarting after a crash
// loop (3) or when another splitdpi already runs (4).
func TestEnableWritesUnit(t *testing.T) {
	o, f := runOpsFor(t, systemdRoot())
	writeConfig(t, o.P, withStrategy)
	installBinary(t, o.P)
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
