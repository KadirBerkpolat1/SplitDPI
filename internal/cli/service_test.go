package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"testing/fstest"

	"github.com/KadirBerkpolat1/SplitDPI/internal/paths"
)

// All of these use fake runners and a temp root: no real service is touched.

var (
	systemdRoot = func() fstest.MapFS { return fstest.MapFS{"run/systemd/system": {Mode: os.ModeDir}} }
	openrcRoot  = func() fstest.MapFS { return fstest.MapFS{"run/openrc": {Mode: os.ModeDir}} }
)

const withStrategy = "[strategy]\nid = \"tt\"\ntcp_tls = \"--payload=tls_client_hello --lua-desync=multisplit:pos=2\"\n"

// holdLock makes the test process look like a running supervisor in mode;
// the returned func releases it, like that supervisor exiting.
func holdLock(t *testing.T, p paths.Paths, mode string) func() {
	t.Helper()
	// A PID that cannot exist: a wrong code path must fail, not signal this test.
	return holdLockAs(t, p, mode, 999999999)
}

// holdLockAs is holdLock with pid recorded as the supervisor's.
func holdLockAs(t *testing.T, p paths.Paths, mode string, pid int) func() {
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

func installBinary(t *testing.T, p paths.Paths) {
	t.Helper()
	os.MkdirAll(filepath.Dir(p.BinPath), 0o755)
	if err := os.WriteFile(p.BinPath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestEnableUnsupportedInit(t *testing.T) {
	o, f := runOpsFor(t, fstest.MapFS{})
	writeConfig(t, o.P, withStrategy)
	installBinary(t, o.P)
	out, code := runOps(t, o, "enable")
	eq(t, code, 1)
	has(t, out, "splitdpi run")
	if len(f.calls) > 0 && slices.ContainsFunc(f.calls, func(c string) bool { return strings.HasPrefix(c, "systemctl") }) {
		t.Errorf("service manager called: %q", f.calls)
	}
}

func TestEnableNoStrategy(t *testing.T) {
	o, _ := runOpsFor(t, systemdRoot())
	installBinary(t, o.P)
	out, code := runOps(t, o, "enable")
	eq(t, code, 1)
	has(t, out, "splitdpi scan")
}

func TestEnableNeedsInstalledBinary(t *testing.T) {
	o, _ := runOpsFor(t, systemdRoot())
	writeConfig(t, o.P, withStrategy)
	out, code := runOps(t, o, "enable")
	eq(t, code, 1)
	has(t, out, o.P.BinPath)
}

func TestEnableSystemd(t *testing.T) {
	o, f := runOpsFor(t, systemdRoot())
	writeConfig(t, o.P, withStrategy)
	installBinary(t, o.P)
	f.hooks = map[string]func(){"systemctl start splitdpi": func() { holdLock(t, o.P, "service") }}
	out, code := runOps(t, o, "enable")
	if code != 0 {
		t.Fatalf("enable exit %d:\n%s", code, out)
	}
	want := []string{"systemctl daemon-reload", "systemctl enable splitdpi", "systemctl start splitdpi"}
	if !slices.Equal(f.calls[len(f.calls)-3:], want) {
		t.Errorf("calls %q", f.calls)
	}
	unit, err := os.ReadFile(o.P.SystemdUnit)
	if err != nil || !strings.Contains(string(unit), "ExecStart="+o.P.BinPath+" run --service") {
		t.Errorf("unit %q, %v", unit, err)
	}
}

func TestDisable(t *testing.T) {
	o, f := runOpsFor(t, systemdRoot())
	os.MkdirAll(filepath.Dir(o.P.SystemdUnit), 0o755)
	os.WriteFile(o.P.SystemdUnit, []byte("[Unit]\n"), 0o644)
	out, code := runOps(t, o, "disable")
	if code != 0 {
		t.Fatalf("disable exit %d:\n%s", code, out)
	}
	if !f.saw("systemctl stop splitdpi") || !f.saw("systemctl disable splitdpi") {
		t.Errorf("calls %q", f.calls)
	}
	if _, err := os.Stat(o.P.SystemdUnit); !os.IsNotExist(err) {
		t.Errorf("unit still there: %v", err)
	}
	out, code = runOps(t, o, "disable")
	eq(t, code, 0)
	has(t, out, "not enabled")
}

func TestRevertRemovesService(t *testing.T) {
	o, f := runOpsFor(t, systemdRoot())
	os.MkdirAll(filepath.Dir(o.P.SystemdUnit), 0o755)
	os.WriteFile(o.P.SystemdUnit, []byte("[Unit]\n"), 0o644)
	out, code := runOps(t, o, "revert")
	eq(t, code, 0)
	if !f.saw("systemctl disable splitdpi") {
		t.Errorf("calls %q", f.calls)
	}
	if _, err := os.Stat(o.P.SystemdUnit); !os.IsNotExist(err) {
		t.Errorf("unit still there: %v", err)
	}
	has(t, out, "clean")
}

// --rules-only runs from inside the service's own stop (ExecStopPost):
// calling the service manager there would deadlock.
func TestRevertRulesOnlyLeavesService(t *testing.T) {
	o, f := runOpsFor(t, systemdRoot())
	os.MkdirAll(filepath.Dir(o.P.SystemdUnit), 0o755)
	os.WriteFile(o.P.SystemdUnit, []byte("[Unit]\n"), 0o644)
	if _, code := runOps(t, o, "revert", "--rules-only"); code != 0 {
		t.Fatalf("code %d", code)
	}
	for _, c := range f.calls {
		if strings.HasPrefix(c, "systemctl") {
			t.Errorf("service manager called from --rules-only: %q", c)
		}
	}
}

func TestStopServiceMode(t *testing.T) {
	o, f := runOpsFor(t, systemdRoot())
	release := holdLock(t, o.P, "service")
	f.hooks = map[string]func(){"systemctl stop splitdpi": release}
	out, code := runOps(t, o, "stop")
	if code != 0 {
		t.Fatalf("stop exit %d:\n%s", code, out)
	}
	if !f.saw("systemctl stop splitdpi") {
		t.Errorf("calls %q", f.calls)
	}
}

func TestLogsFromFile(t *testing.T) {
	o, _ := runOpsFor(t, openrcRoot())
	os.MkdirAll(filepath.Dir(o.P.LogFile), 0o755)
	os.WriteFile(o.P.LogFile+".1", []byte("older\n"), 0o644)
	os.WriteFile(o.P.LogFile, []byte("newer\n"), 0o644)
	out, code := runOps(t, o, "logs")
	eq(t, code, 0)
	if !strings.Contains(out, "older\nnewer\n") {
		t.Errorf("got %q", out)
	}
}

func TestLogsFromJournal(t *testing.T) {
	o, f := runOpsFor(t, systemdRoot())
	f.out = map[string]string{"journalctl -u splitdpi --no-pager": "journal line\n"}
	out, code := runOps(t, o, "logs")
	eq(t, code, 0)
	has(t, out, "journal line")
}

func TestServiceUsage(t *testing.T) {
	for _, args := range [][]string{{"enable", "x"}, {"disable", "x"}, {"logs", "x"}} {
		if _, code := run(t, 0, args...); code != 2 {
			t.Errorf("%v: code %d, want 2", args, code)
		}
	}
}
