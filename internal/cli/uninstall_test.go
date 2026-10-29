package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUninstallWithoutYesChangesNothing(t *testing.T) {
	o, f := runOpsFor(t, systemdRoot())
	writeConfig(t, o.P, withStrategy)
	installBinary(t, o.P)
	os.MkdirAll(filepath.Dir(o.P.SystemdUnit), 0o755)
	os.WriteFile(o.P.SystemdUnit, []byte("[Unit]\n"), 0o644)
	out, code := runOps(t, o, "uninstall")
	eq(t, code, 0)
	has(t, out, o.P.ConfigDir)
	has(t, out, o.P.BinPath)
	has(t, out, "splitdpi service (systemd)")
	has(t, out, "sudo splitdpi uninstall --yes")
	if len(f.calls) != 0 {
		t.Errorf("commands run without --yes: %q", f.calls)
	}
	for _, p := range []string{o.P.ConfigFile, o.P.BinPath, o.P.SystemdUnit} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s changed without --yes: %v", p, err)
		}
	}
}

func TestUninstallNothingInstalled(t *testing.T) {
	o, _ := runOpsFor(t, systemdRoot())
	out, code := runOps(t, o, "uninstall")
	eq(t, code, 0)
	has(t, out, "no service or files")
}

func TestUninstallYes(t *testing.T) {
	o, f := runOpsFor(t, systemdRoot())
	writeConfig(t, o.P, withStrategy)
	installBinary(t, o.P)
	os.MkdirAll(filepath.Dir(o.P.SystemdUnit), 0o755)
	os.WriteFile(o.P.SystemdUnit, []byte("[Unit]\n"), 0o644)
	out, code := runOps(t, o, "uninstall", "--yes")
	if code != 0 {
		t.Fatalf("uninstall exit %d:\n%s", code, out)
	}
	if !f.saw("systemctl disable splitdpi") {
		t.Errorf("calls %q", f.calls)
	}
	has(t, out, "Removed "+o.P.BinPath)
	has(t, out, "uninstalled")
	for _, p := range []string{o.P.ConfigDir, o.P.BinPath, o.P.SystemdUnit} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s still there: %v", p, err)
		}
	}
}

func TestUninstallLeftoverFails(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	o, _ := runOpsFor(t, systemdRoot())
	installBinary(t, o.P)
	logDir := filepath.Dir(o.P.LogFile)
	os.MkdirAll(logDir, 0o755)
	os.WriteFile(o.P.LogFile, []byte("x\n"), 0o644)
	os.Chmod(logDir, 0o555)
	t.Cleanup(func() { os.Chmod(logDir, 0o755) })
	out, code := runOps(t, o, "uninstall", "--yes")
	eq(t, code, 1)
	has(t, out, "Still present: "+o.P.LogFile)
	has(t, out, "Still present: "+o.P.BinPath)
	has(t, out, "did not finish")
}

func TestUninstallUsage(t *testing.T) {
	if _, code := run(t, 0, "uninstall", "--bogus"); code != 2 {
		t.Errorf("code %d, want 2", code)
	}
}
