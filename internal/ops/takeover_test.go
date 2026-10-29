package ops

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/KadirBerkpolat1/SplitDPI/internal/initsys"
	"github.com/KadirBerkpolat1/SplitDPI/internal/paths"
	"github.com/KadirBerkpolat1/SplitDPI/internal/sysinfo"
)

// fakeInit records commands; out maps a command to its stdout, and any output
// but "enabled" also fails, like systemctl is-enabled. nft reports no table,
// so Revert sees a clean system apart from the takeover record.
type fakeInit struct {
	calls []string
	out   map[string]string
}

func (f *fakeInit) run(_ context.Context, _ string, name string, args ...string) ([]byte, error) {
	cmd := strings.Join(append([]string{name}, args...), " ")
	if name == "nft" {
		return nil, errors.New("No such file or directory")
	}
	f.calls = append(f.calls, cmd)
	if out, ok := f.out[cmd]; ok && out != "enabled" {
		return []byte(out), errors.New("exit status 1")
	}
	return []byte(f.out[cmd]), nil
}

func takeoverOps(t *testing.T, root fstest.MapFS, out map[string]string) (*Ops, *fakeInit) {
	f := &fakeInit{out: out}
	return &Ops{P: paths.Under(t.TempDir()), Exec: f.run, Root: root}, f
}

func proc(pid, comm string) *fstest.MapFile {
	return &fstest.MapFile{Data: []byte(pid + " (" + comm + ") S 1 1 1 0 -1 0")}
}

func sites(t *testing.T, o *Ops) []string {
	t.Helper()
	l, err := o.SitesList()
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func TestSitesImport(t *testing.T) {
	o := &Ops{P: paths.Under(t.TempDir())}
	if _, err := o.SitesAdd("discord.com"); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "list.txt")
	in := "\ufeff# my sites\r\n" +
		"pastebin.com  # trailing comment\n" +
		"\n" +
		"*.Example.ORG\n" +
		".rutracker.org\n" +
		"discord.com\n" +
		"not a domain\n" +
		"example.org\n" +
		"1.2.3.4\n"
	if err := os.WriteFile(file, []byte(in), 0o644); err != nil {
		t.Fatal(err)
	}
	added, skipped, err := o.SitesImport(file)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"pastebin.com", "example.org", "rutracker.org"}; !slices.Equal(added, want) {
		t.Errorf("added %q, want %q", added, want)
	}
	if want := []ImportSkip{{7, "not a domain"}, {9, "1.2.3.4"}}; !slices.Equal(skipped, want) {
		t.Errorf("skipped %+v, want %+v", skipped, want)
	}
	if want := []string{"discord.com", "pastebin.com", "example.org", "rutracker.org"}; !slices.Equal(sites(t, o), want) {
		t.Errorf("sites %q", sites(t, o))
	}
	if _, _, err := o.SitesImport(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Error("missing file imported")
	}
}

func TestDetectForeignNone(t *testing.T) {
	o, _ := takeoverOps(t, fstest.MapFS{"run/systemd/system": {Mode: os.ModeDir}}, nil)
	found, err := o.DetectForeign(context.Background())
	if err != nil || len(found) != 0 {
		t.Fatalf("got %+v, %v", found, err)
	}
}

// During a scan blockcheck2 runs splitdpi's own nfqws2; that is not a zapret2.
func TestDetectForeignSkipsEngineNfqws2(t *testing.T) {
	o, _ := takeoverOps(t, nil, nil)
	exe := filepath.Join(o.P.EngineDir, "zapret2-v1.0.6/binaries/linux-x86_64/nfqws2")
	o.Root = fstest.MapFS{
		"run/systemd/system": {Mode: os.ModeDir},
		"proc/700/stat":      proc("700", "nfqws2"),
		"proc/700/exe":       {Mode: fs.ModeSymlink, Data: []byte(exe)},
	}
	found, err := o.DetectForeign(context.Background())
	if err != nil || len(found) != 0 {
		t.Fatalf("found %+v, %v", found, err)
	}
}

func systemdZapret2() fstest.MapFS {
	return fstest.MapFS{
		"run/systemd/system":                      {Mode: os.ModeDir},
		"usr/lib/systemd/system/zapret2.service":  {},
		"opt/zapret2/ipset/zapret-hosts-user.txt": {Data: []byte("# Pastebin\npastebin.com\n*.pastebin.com\n\ndiscord.com\n")},
		"proc/1156/stat":                          proc("1156", "nfqws2"),
	}
}

func TestTakeoverSystemdAndRevert(t *testing.T) {
	ctx := context.Background()
	o, f := takeoverOps(t, systemdZapret2(), map[string]string{"systemctl is-enabled zapret2": "enabled"})
	found, err := o.DetectForeign(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := Foreign{
		Kind:     "zapret2",
		Dir:      "/opt/zapret2",
		Service:  initsys.ForeignService{Init: sysinfo.Systemd, Name: "zapret2", Enabled: true},
		Hostlist: "/opt/zapret2/ipset/zapret-hosts-user.txt",
		Running:  true,
	}
	if len(found) != 1 || !equalForeign(found[0], want) {
		t.Fatalf("found %+v", found)
	}
	r, err := o.Takeover(ctx, found[0])
	if err != nil {
		t.Fatal(err)
	}
	if !r.Stopped || !r.Disabled || !slices.Equal(r.Added, []string{"pastebin.com", "discord.com"}) {
		t.Errorf("report %+v", r)
	}
	for _, c := range []string{"systemctl stop zapret2", "systemctl disable zapret2"} {
		if !slices.Contains(f.calls, c) {
			t.Errorf("no %q in %q", c, f.calls)
		}
	}

	// A second takeover sees it stopped and disabled, and must not forget that it was not.
	stopped := found[0]
	stopped.Running, stopped.Service.Enabled = false, false
	if _, err := o.Takeover(ctx, stopped); err != nil {
		t.Fatal(err)
	}

	f.calls = nil
	rep := o.Revert(ctx, false, false)
	if !slices.Equal(f.calls, []string{"systemctl enable zapret2", "systemctl start zapret2"}) {
		t.Errorf("revert calls %q", f.calls)
	}
	if !slices.Equal(rep.Restored, []string{"zapret2 (systemd)"}) || len(rep.Leftover) != 0 {
		t.Errorf("revert %+v", rep)
	}
	if _, err := os.Stat(o.P.TakeoverFile); !os.IsNotExist(err) {
		t.Errorf("record still there: %v", err)
	}
	f.calls = nil
	if rep := o.Revert(ctx, false, false); len(rep.Restored) != 0 || len(f.calls) != 0 {
		t.Errorf("second revert restored again: %+v, %q", rep, f.calls)
	}
}

func equalForeign(a, b Foreign) bool {
	return a.Kind == b.Kind && a.Dir == b.Dir && a.Hostlist == b.Hostlist && a.Running == b.Running &&
		a.Service.Init == b.Service.Init && a.Service.Name == b.Service.Name && a.Service.Enabled == b.Service.Enabled &&
		slices.Equal(a.Service.Runlevels, b.Service.Runlevels) && a.Service.Link == b.Service.Link && a.Service.Target == b.Service.Target
}

func TestTakeoverOpenRC(t *testing.T) {
	ctx := context.Background()
	o, f := takeoverOps(t, fstest.MapFS{
		"run/openrc":                   {Mode: os.ModeDir},
		"etc/init.d/zapret":            {},
		"etc/runlevels/default/zapret": {},
		"opt/zapret/ipset":             {Mode: os.ModeDir},
		"proc/900/stat":                proc("900", "tpws"),
	}, nil)
	found, err := o.DetectForeign(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := Foreign{
		Kind: "zapret", Dir: "/opt/zapret", Running: true,
		Service: initsys.ForeignService{Init: sysinfo.OpenRC, Name: "zapret", Enabled: true, Runlevels: []string{"default"}},
	}
	if len(found) != 1 || !equalForeign(found[0], want) {
		t.Fatalf("found %+v", found)
	}
	if _, err := o.Takeover(ctx, found[0]); err != nil {
		t.Fatal(err)
	}
	if want := []string{"rc-service zapret stop", "rc-update del zapret default"}; !slices.Equal(f.calls, want) {
		t.Errorf("takeover calls %q", f.calls)
	}
	f.calls = nil
	o.Revert(ctx, false, false)
	if want := []string{"rc-update add zapret default", "rc-service zapret start"}; !slices.Equal(f.calls, want) {
		t.Errorf("revert calls %q", f.calls)
	}
}

// Runit's enable link is a real symlink, so this one uses a real directory as Root.
func TestTakeoverRunit(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	for _, d := range []string{"etc/runit/runsvdir/default", "var/service", "opt/zapret/ipset", "proc/77"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	const target = "/opt/zapret/init.d/runit/zapret"
	link := filepath.Join(dir, "var/service/zapret")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	// Artix's runsvdir/current points at a runsvdir: it must not be a second find.
	if err := os.Symlink("default", filepath.Join(dir, "etc/runit/runsvdir/current")); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "proc/77/stat"), []byte("77 (nfqws) S 1 1 1 0 -1 0"), 0o644)
	os.WriteFile(filepath.Join(dir, "opt/zapret/ipset/zapret-hosts-user.txt"), []byte("youtube.com\n"), 0o644)

	f := &fakeInit{}
	o := &Ops{P: paths.Under(dir), Exec: f.run, Root: os.DirFS(dir)}
	found, err := o.DetectForeign(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := Foreign{
		Kind: "zapret", Dir: "/opt/zapret", Running: true, Hostlist: "/opt/zapret/ipset/zapret-hosts-user.txt",
		Service: initsys.ForeignService{Init: sysinfo.Runit, Name: "zapret", Enabled: true, Link: "/var/service/zapret", Target: target},
	}
	if len(found) != 1 || !equalForeign(found[0], want) {
		t.Fatalf("found %+v", found)
	}
	if _, err := o.Takeover(ctx, found[0]); err != nil {
		t.Fatal(err)
	}
	if want := []string{"sv down " + link}; !slices.Equal(f.calls, want) {
		t.Errorf("takeover calls %q", f.calls)
	}
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Errorf("enable link still there: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "opt/zapret/ipset/zapret-hosts-user.txt")); err != nil {
		t.Errorf("their hostlist was touched: %v", err)
	}
	rep := o.Revert(ctx, false, false)
	if got, err := os.Readlink(link); err != nil || got != target {
		t.Errorf("link after revert: %q, %v", got, err)
	}
	if !slices.Equal(rep.Restored, []string{"zapret (runit)"}) {
		t.Errorf("revert %+v", rep)
	}
}

func TestTakeoverStoppedInstall(t *testing.T) {
	root := systemdZapret2()
	delete(root, "proc/1156/stat")
	o, f := takeoverOps(t, root, map[string]string{"systemctl is-enabled zapret2": "disabled"})
	found, err := o.DetectForeign(context.Background())
	if err != nil || len(found) != 1 || found[0].Running || found[0].Service.Enabled || found[0].Service.Name != "zapret2" {
		t.Fatalf("found %+v, %v", found, err)
	}
	r, err := o.Takeover(context.Background(), found[0])
	if err != nil || r.Stopped || !r.Disabled || len(r.Added) != 2 {
		t.Fatalf("report %+v, %v", r, err)
	}
	// Disabling it again changes nothing, and there is nothing to restore.
	if want := []string{"systemctl is-enabled zapret2", "systemctl disable zapret2"}; !slices.Equal(f.calls, want) {
		t.Errorf("calls %q, want %q", f.calls, want)
	}
	if _, err := os.Stat(o.P.TakeoverFile); !os.IsNotExist(err) {
		t.Errorf("record written for nothing turned off: %v", err)
	}
}

// When systemctl cannot say whether the service starts on boot, takeover
// still disables it, and revert enables it.
func TestTakeoverSystemdBootUnknown(t *testing.T) {
	ctx := context.Background()
	root := systemdZapret2()
	delete(root, "proc/1156/stat")
	o, f := takeoverOps(t, root, map[string]string{"systemctl is-enabled zapret2": ""})
	found, err := o.DetectForeign(ctx)
	if err != nil || len(found) != 1 {
		t.Fatalf("found %+v, %v", found, err)
	}
	if s := found[0].Service; s.Enabled || !s.BootUnknown || !s.MayStartOnBoot() {
		t.Fatalf("service %+v; want its boot state unknown", s)
	}
	r, err := o.Takeover(ctx, found[0])
	if err != nil || !r.Disabled || r.Stopped {
		t.Fatalf("report %+v, %v", r, err)
	}
	if !slices.Contains(f.calls, "systemctl disable zapret2") {
		t.Errorf("not disabled: %q", f.calls)
	}
	f.calls = nil
	rep := o.Revert(ctx, false, false)
	if want := []string{"systemctl enable zapret2"}; !slices.Equal(f.calls, want) {
		t.Errorf("revert calls %q, want %q", f.calls, want)
	}
	if !slices.Equal(rep.Restored, []string{"zapret2 (systemd)"}) {
		t.Errorf("revert %+v", rep)
	}
}

// Every state systemctl is-enabled names is known, and only "enabled" starts on boot.
func TestSystemdBootStates(t *testing.T) {
	for out, want := range map[string]initsys.ForeignService{
		"enabled\n":                {Enabled: true},
		"disabled\n":               {},
		"static\n":                 {},
		"masked\n":                 {},
		"":                         {BootUnknown: true},
		"Failed to connect to bus": {BootUnknown: true},
	} {
		o, _ := takeoverOps(t, systemdZapret2(), map[string]string{"systemctl is-enabled zapret2": out})
		found, err := o.DetectForeign(context.Background())
		if err != nil || len(found) != 1 {
			t.Fatalf("%q: found %+v, %v", out, found, err)
		}
		if s := found[0].Service; s.Enabled != want.Enabled || s.BootUnknown != want.BootUnknown {
			t.Errorf("is-enabled %q: enabled %v unknown %v, want %v %v", out, s.Enabled, s.BootUnknown, want.Enabled, want.BootUnknown)
		}
	}
}

func TestTakeoverGzipHostlist(t *testing.T) {
	var gz bytes.Buffer
	z := gzip.NewWriter(&gz)
	z.Write([]byte("rutracker.org\n*.nnmclub.to\n"))
	z.Close()
	root := systemdZapret2()
	root["opt/zapret2/ipset/zapret-hosts-user.txt.gz"] = &fstest.MapFile{Data: gz.Bytes()}
	o, _ := takeoverOps(t, root, nil)
	found, err := o.DetectForeign(context.Background())
	if err != nil || len(found) != 1 || found[0].Hostlist != "/opt/zapret2/ipset/zapret-hosts-user.txt.gz" {
		t.Fatalf("found %+v, %v", found, err)
	}
	r, err := o.Takeover(context.Background(), found[0])
	if err != nil || !slices.Equal(r.Added, []string{"rutracker.org", "nnmclub.to"}) {
		t.Fatalf("report %+v, %v", r, err)
	}
}

// A purge (uninstall) must keep the record of a service it failed to turn
// back on, so the next run can still restore it.
func TestPurgeKeepsUnrestoredTakeover(t *testing.T) {
	ctx := context.Background()
	o, f := takeoverOps(t, systemdZapret2(), map[string]string{"systemctl is-enabled zapret2": "enabled"})
	found, err := o.DetectForeign(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := o.Takeover(ctx, found[0]); err != nil {
		t.Fatal(err)
	}
	f.out["systemctl enable zapret2"] = "Failed to enable unit: Access denied"
	rep := o.Revert(ctx, false, true)
	if !slices.ContainsFunc(rep.Leftover, func(l string) bool { return strings.HasPrefix(l, "zapret2 (systemd): ") }) {
		t.Errorf("failed restore not reported: %+v", rep)
	}
	if _, err := os.Stat(o.P.TakeoverFile); err != nil {
		t.Fatalf("purge removed the record of a service still off: %v", err)
	}
	if _, err := os.Stat(o.P.ConfigDir); !os.IsNotExist(err) {
		t.Errorf("config dir not purged: %v", err)
	}

	delete(f.out, "systemctl enable zapret2")
	f.calls = nil
	rep = o.Revert(ctx, false, true)
	if !slices.Equal(f.calls, []string{"systemctl enable zapret2", "systemctl start zapret2"}) || len(rep.Leftover) != 0 {
		t.Errorf("second purge: calls %q, report %+v", f.calls, rep)
	}
	if _, err := os.Stat(o.P.StateDir); !os.IsNotExist(err) {
		t.Errorf("state dir not purged once restored: %v", err)
	}
}

func TestCorruptTakeoverRecord(t *testing.T) {
	ctx := context.Background()
	o, f := takeoverOps(t, systemdZapret2(), map[string]string{"systemctl is-enabled zapret2": "enabled"})
	os.MkdirAll(o.P.StateDir, 0o755)
	os.WriteFile(o.P.TakeoverFile, []byte("{not json"), 0o644)

	rep := o.Revert(ctx, false, false)
	if len(rep.Leftover) != 1 || !strings.Contains(rep.Leftover[0], "takeover.json") {
		t.Errorf("revert %+v", rep)
	}
	if _, err := os.Stat(o.P.TakeoverFile); err != nil {
		t.Errorf("corrupt record removed: %v", err)
	}

	found, _ := o.DetectForeign(ctx)
	f.calls = nil
	if _, err := o.Takeover(ctx, found[0]); err == nil {
		t.Error("takeover overwrote a corrupt record")
	}
	if len(f.calls) != 0 {
		t.Errorf("service touched without a record: %q", f.calls)
	}
}
