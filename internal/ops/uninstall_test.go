package ops

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/KadirBerkpolat1/SplitDPI/internal/paths"
)

var systemdHost = fstest.MapFS{"run/systemd/system": {Mode: os.ModeDir}}

// uninstallOps has every SplitDPI path populated, plus unrelated files that
// must survive; it returns the ops, the unrelated files and the commands run.
func uninstallOps(t *testing.T, host fstest.MapFS) (*Ops, []string, *[]string) {
	t.Helper()
	root := t.TempDir()
	p := paths.Under(root)
	unrelated := []string{
		filepath.Join(root, "etc/other.conf"),
		filepath.Join(root, "usr/local/bin/other"),
		filepath.Join(root, "var/log/syslog"),
		filepath.Join(root, "var/lib/other/data"),
	}
	for _, f := range append([]string{
		p.ConfigFile, filepath.Join(p.EngineDir, "current"), p.StatusFile,
		p.LogFile, p.LogFile + ".1", p.BinPath,
	}, unrelated...) {
		if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f, []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var calls []string
	o := &Ops{
		P: p, Root: host, Euid: func() int { return 0 }, GOARCH: "amd64",
		Exec: func(_ context.Context, _ string, name string, args ...string) ([]byte, error) {
			calls = append(calls, strings.Join(append([]string{name}, args...), " "))
			if name == "nft" {
				return nil, errors.New("Error: No such file or directory")
			}
			return nil, nil
		},
	}
	return o, unrelated, &calls
}

func installUnit(t *testing.T, p paths.Paths) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p.SystemdUnit), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.SystemdUnit, []byte("[Unit]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestUninstallPlanListsOnlyOurPaths(t *testing.T) {
	o, _, calls := uninstallOps(t, fstest.MapFS{})
	plan := o.UninstallPlan()
	want := []string{o.P.ConfigDir, o.P.StateDir, o.P.RunDir, o.P.LogFile, o.P.LogFile + ".1", o.P.BinPath}
	if !slices.Equal(plan.Paths, want) {
		t.Errorf("paths %q, want %q", plan.Paths, want)
	}
	if plan.Service != "" {
		t.Errorf("service %q without an installed service", plan.Service)
	}
	for _, f := range want {
		if _, err := os.Stat(f); err != nil {
			t.Errorf("plan touched %s: %v", f, err)
		}
	}
	if len(*calls) != 0 {
		t.Errorf("plan ran %q", *calls)
	}
}

func TestUninstallPlanService(t *testing.T) {
	o, _, _ := uninstallOps(t, systemdHost)
	installUnit(t, o.P)
	if got := o.UninstallPlan().Service; got != serviceLabel(o) {
		t.Errorf("service %q, want %q", got, serviceLabel(o))
	}
}

func TestUninstallPlanEmpty(t *testing.T) {
	o := &Ops{P: paths.Under(t.TempDir()), Root: fstest.MapFS{}}
	if plan := o.UninstallPlan(); plan.Service != "" || len(plan.Paths) != 0 {
		t.Errorf("plan %+v on an empty root", plan)
	}
}

func TestUninstallRemovesEverything(t *testing.T) {
	o, unrelated, calls := uninstallOps(t, systemdHost)
	installUnit(t, o.P)
	r, err := o.Uninstall(context.Background())
	if err != nil {
		t.Fatalf("uninstall: %v, leftover %q", err, r.Leftover)
	}
	if !slices.Contains(*calls, "systemctl disable splitdpi") {
		t.Errorf("service not disabled; calls %q", *calls)
	}
	for _, f := range []string{o.P.SystemdUnit, o.P.ConfigDir, o.P.StateDir, o.P.RunDir, o.P.LogFile, o.P.LogFile + ".1", o.P.BinPath} {
		if _, err := os.Lstat(f); !os.IsNotExist(err) {
			t.Errorf("%s still there: %v", f, err)
		}
	}
	for _, f := range unrelated {
		if _, err := os.Stat(f); err != nil {
			t.Errorf("unrelated %s removed: %v", f, err)
		}
	}
	if len(r.Removed) == 0 || r.Removed[len(r.Removed)-1] != o.P.BinPath {
		t.Errorf("removed %q, want the binary last", r.Removed)
	}
	if !slices.Contains(r.Removed, serviceLabel(o)) {
		t.Errorf("removed %q lacks the service", r.Removed)
	}
}

// A path that cannot be removed is reported, and the binary stays so
// uninstall can be run again.
func TestUninstallPartialFailure(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	o, _, _ := uninstallOps(t, fstest.MapFS{})
	logDir := filepath.Dir(o.P.LogFile)
	if err := os.Chmod(logDir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(logDir, 0o755) })
	r, err := o.Uninstall(context.Background())
	if !errors.Is(err, ErrUninstallIncomplete) {
		t.Fatalf("err %v, want ErrUninstallIncomplete", err)
	}
	want := []string{o.P.LogFile, o.P.LogFile + ".1", o.P.BinPath}
	if !slices.Equal(r.Leftover, want) {
		t.Errorf("leftover %q, want %q", r.Leftover, want)
	}
	if _, err := os.Stat(o.P.BinPath); err != nil {
		t.Errorf("binary removed despite leftovers: %v", err)
	}
	if _, err := os.Stat(o.P.ConfigDir); !os.IsNotExist(err) {
		t.Errorf("config dir still there: %v", err)
	}
}
