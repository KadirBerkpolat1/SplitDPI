package initsys_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/KadirBerkpolat1/SplitDPI/internal/execx"
	"github.com/KadirBerkpolat1/SplitDPI/internal/initsys"
	"github.com/KadirBerkpolat1/SplitDPI/internal/paths"
	"github.com/KadirBerkpolat1/SplitDPI/internal/sysinfo"
)

const bin = "/usr/local/bin/splitdpi"

type recorder struct {
	calls []string
}

func (r *recorder) run(_ context.Context, _ string, name string, args ...string) ([]byte, error) {
	r.calls = append(r.calls, strings.Join(append([]string{name}, args...), " "))
	return nil, nil
}

func readGolden(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func eqFile(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("%s:\n got %q\nwant %q", path, got, want)
	}
}

func manager(t *testing.T, kind sysinfo.Init, run execx.Runner) (initsys.Manager, paths.Paths) {
	t.Helper()
	p := paths.Under(t.TempDir())
	m, err := initsys.For(kind, run, bin, p)
	if err != nil {
		t.Fatal(err)
	}
	return m, p
}

func TestForUnsupported(t *testing.T) {
	_, err := initsys.For(sysinfo.OtherInit, execx.Real, bin, paths.Under(t.TempDir()))
	if err != initsys.ErrUnsupportedInit {
		t.Fatalf("got %v", err)
	}
}

func TestSystemdInstallGolden(t *testing.T) {
	m, p := manager(t, sysinfo.Systemd, execx.Real)
	if err := m.Install(); err != nil {
		t.Fatal(err)
	}
	eqFile(t, p.SystemdUnit, readGolden(t, "splitdpi.service.golden"))
}

func TestOpenRCInstallGolden(t *testing.T) {
	m, p := manager(t, sysinfo.OpenRC, execx.Real)
	if err := m.Install(); err != nil {
		t.Fatal(err)
	}
	eqFile(t, p.OpenRCScript, readGolden(t, "splitdpi.openrc.golden"))
	fi, _ := os.Stat(p.OpenRCScript)
	if fi.Mode().Perm() != 0o755 {
		t.Fatalf("mode %o", fi.Mode().Perm())
	}
}

func TestRunitInstallGolden(t *testing.T) {
	m, p := manager(t, sysinfo.Runit, execx.Real)
	if err := m.Install(); err != nil {
		t.Fatal(err)
	}
	eqFile(t, filepath.Join(p.RunitDir, "run"), readGolden(t, "runit-run.golden"))
	eqFile(t, filepath.Join(p.RunitDir, "finish"), readGolden(t, "runit-finish.golden"))
}

func TestSystemdEnableDisable(t *testing.T) {
	rec := &recorder{}
	m, _ := manager(t, sysinfo.Systemd, rec.run)
	ctx := context.Background()
	for _, fn := range []func(context.Context) error{m.Enable, m.Start, m.Stop, m.Disable} {
		if err := fn(ctx); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{
		"systemctl daemon-reload",
		"systemctl enable splitdpi",
		"systemctl start splitdpi",
		"systemctl stop splitdpi",
		"systemctl disable splitdpi",
	}
	if strings.Join(rec.calls, "|") != strings.Join(want, "|") {
		t.Fatalf("calls %v", rec.calls)
	}
}

func TestOpenRCEnableDisable(t *testing.T) {
	rec := &recorder{}
	m, _ := manager(t, sysinfo.OpenRC, rec.run)
	ctx := context.Background()
	for _, fn := range []func(context.Context) error{m.Enable, m.Start, m.Stop, m.Disable} {
		if err := fn(ctx); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{
		"rc-update add splitdpi default",
		"rc-service splitdpi start",
		"rc-service splitdpi stop",
		"rc-update del splitdpi default",
	}
	if strings.Join(rec.calls, "|") != strings.Join(want, "|") {
		t.Fatalf("calls %v", rec.calls)
	}
}

func TestRunitEnableRemove(t *testing.T) {
	rec := &recorder{}
	m, p := manager(t, sysinfo.Runit, rec.run)
	if err := m.Install(); err != nil {
		t.Fatal(err)
	}
	if err := m.Enable(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p.RunitLink); err != nil {
		t.Fatal(err)
	}
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := m.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := m.Disable(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p.RunitLink); !os.IsNotExist(err) {
		t.Fatalf("link still exists")
	}
	if err := m.Remove(); err != nil {
		t.Fatal(err)
	}
	if m.Installed() {
		t.Fatal("still installed")
	}
	if strings.Join(rec.calls, "|") != "sv up splitdpi|sv down splitdpi" {
		t.Fatalf("calls %v", rec.calls)
	}
}

// revert and disable work on a fresh manager, not the one that installed.
func TestRunitFreshManagerDisables(t *testing.T) {
	p := paths.Under(t.TempDir())
	installer, _ := initsys.For(sysinfo.Runit, (&recorder{}).run, bin, p)
	if err := installer.Install(); err != nil {
		t.Fatal(err)
	}
	if err := installer.Enable(context.Background()); err != nil {
		t.Fatal(err)
	}
	fresh, _ := initsys.For(sysinfo.Runit, (&recorder{}).run, bin, p)
	if !fresh.Installed() {
		t.Fatal("fresh manager does not see the install")
	}
	if err := fresh.Disable(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(p.RunitLink); !os.IsNotExist(err) {
		t.Error("link still exists")
	}
}

func TestSystemdLogsCmd(t *testing.T) {
	m, _ := manager(t, sysinfo.Systemd, execx.Real)
	if got := strings.Join(m.LogsCmd(false), " "); got != "journalctl -u splitdpi --no-pager" {
		t.Fatalf("got %q", got)
	}
	if got := strings.Join(m.LogsCmd(true), " "); got != "journalctl -u splitdpi --no-pager -f" {
		t.Fatalf("got %q", got)
	}
}

func TestSystemdRemove(t *testing.T) {
	m, _ := manager(t, sysinfo.Systemd, execx.Real)
	_ = m.Install()
	if err := m.Remove(); err != nil {
		t.Fatal(err)
	}
	if m.Installed() {
		t.Fatal("still installed")
	}
}
