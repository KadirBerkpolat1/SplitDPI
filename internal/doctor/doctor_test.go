package doctor_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"testing/fstest"

	"github.com/KadirBerkpolat1/SplitDPI/internal/doctor"
	"github.com/KadirBerkpolat1/SplitDPI/internal/i18n"
	"github.com/KadirBerkpolat1/SplitDPI/internal/paths"
	"github.com/KadirBerkpolat1/SplitDPI/internal/probe"
)

func TestMain(m *testing.M) {
	i18n.Init("en")
	os.Exit(m.Run())
}

// fake is a clean debian13 machine: root, every tool present, engine installed.
type fake struct {
	env   doctor.Env
	root  fstest.MapFS
	out   map[string]string // "name arg0" -> stdout
	fail  map[string]string // "name arg0" -> error text
	calls []string
}

func newFake(t *testing.T) *fake {
	t.Helper()
	osr, err := os.ReadFile("../sysinfo/testdata/os-release/debian13")
	if err != nil {
		t.Fatal(err)
	}
	f := &fake{
		root: fstest.MapFS{
			"etc/os-release":            {Data: osr},
			"proc/sys/kernel/osrelease": {Data: []byte("6.12.48+deb13-amd64\n")},
			"run/systemd/system":        {Mode: os.ModeDir},
		},
		out: map[string]string{
			"lsattr /etc/resolv.conf": "--------------e------- /etc/resolv.conf\n",
			"ip -6":                   "default via fe80::1 dev eth0\n",
		},
		fail: map[string]string{},
	}
	p := paths.Under(t.TempDir())
	installEngine(t, p, "zapret2-v1.0.6")
	f.env = doctor.Env{
		Root:     f.root,
		P:        p,
		Run:      f.run,
		LookPath: func(name string) (string, error) { return "/usr/sbin/" + name, nil },
		Procs:    func() ([]doctor.Proc, error) { return procs("systemd", "bash", "sshd"), nil },
		Euid:     func() int { return 0 },
	}
	return f
}

func (f *fake) run(_ context.Context, stdin, name string, args ...string) ([]byte, error) {
	key := name
	if len(args) > 0 {
		key += " " + args[0]
	}
	f.calls = append(f.calls, strings.TrimSpace(name+" "+strings.Join(args, " ")))
	if msg, ok := f.fail[key]; ok {
		return nil, errors.New(msg)
	}
	return []byte(f.out[key]), nil
}

// procs fakes unrelated processes: PIDs 100, 101, ... all children of init.
func procs(names ...string) []doctor.Proc {
	var ps []doctor.Proc
	for i, n := range names {
		ps = append(ps, doctor.Proc{PID: 100 + i, PPID: 1, Comm: n})
	}
	return ps
}

// holdLock makes the test process look like a running supervisor with child childPID.
func holdLock(t *testing.T, p paths.Paths, childPID int) {
	t.Helper()
	if err := os.MkdirAll(p.RunDir, 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(p.LockFile, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	if _, err := fmt.Fprintf(f, `{"pid":%d,"mode":"foreground"}`, os.Getpid()); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	st := fmt.Sprintf(`{"state":"running","pid":%d,"child_pid":%d}`, os.Getpid(), childPID)
	if err := os.WriteFile(p.StatusFile, []byte(st), 0o644); err != nil {
		t.Fatal(err)
	}
}

func installEngine(t *testing.T, p paths.Paths, dir string) {
	t.Helper()
	bin := filepath.Join(p.EngineDir, dir, "binaries", "linux-x86_64")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "nfqws2"), nil, 0o755); err != nil {
		t.Fatal(err)
	}
	cur := dir + "\nlinux-x86_64\n"
	if err := os.WriteFile(filepath.Join(p.EngineDir, "current"), []byte(cur), 0o644); err != nil {
		t.Fatal(err)
	}
}

func find(t *testing.T, cs []doctor.Check, id string) []doctor.Check {
	t.Helper()
	var got []doctor.Check
	for _, c := range cs {
		if c.ID == id {
			got = append(got, c)
		}
	}
	if len(got) == 0 {
		t.Fatalf("no %q check in %+v", id, cs)
	}
	return got
}

func one(t *testing.T, cs []doctor.Check, id string, sev doctor.Severity) doctor.Check {
	t.Helper()
	got := find(t, cs, id)
	if len(got) != 1 || got[0].Severity != sev {
		t.Fatalf("%s: got %+v, want one check with severity %v", id, got, sev)
	}
	return got[0]
}

func has(t *testing.T, s, sub string) {
	t.Helper()
	if !strings.Contains(s, sub) {
		t.Errorf("%q does not contain %q", s, sub)
	}
}

func eq(t *testing.T, got, want string) {
	t.Helper()
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestDNSPoisoned(t *testing.T) {
	f := newFake(t)
	f.env.DNSCheck = func(context.Context) []probe.DNSResult {
		return []probe.DNSResult{{
			Domain:   "discord.com",
			Poisoned: true,
			Reason:   "known block page",
		}}
	}
	c := one(t, doctor.Run(context.Background(), f.env), "dns", doctor.Warn)
	eq(t, c.Fix, "sudo splitdpi dns on")
}

func TestStrategyOldEngine(t *testing.T) {
	f := newFake(t)
	cfg := "[strategy]\nid=\"x\"\nengine=\"v1.0.5\"\n"
	if err := os.MkdirAll(f.env.P.ConfigDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.env.P.ConfigFile, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	c := one(t, doctor.Run(context.Background(), f.env), "strategy", doctor.Warn)
	eq(t, c.Fix, "sudo splitdpi scan")
}

func TestClean(t *testing.T) {
	f := newFake(t)
	cs := doctor.Run(context.Background(), f.env)
	for _, id := range []string{"root", "kernel", "nft", "curl", "nfqueue", "engine", "strategy", "lsm", "conflict", "init", "ipv6"} {
		one(t, cs, id, doctor.OK)
	}
	if doctor.ExitCode(cs) != 0 {
		t.Errorf("exit code %d, want 0: %+v", doctor.ExitCode(cs), cs)
	}
}

func TestNoNft(t *testing.T) {
	f := newFake(t)
	f.env.LookPath = func(name string) (string, error) {
		if name == "nft" {
			return "", errors.New("not found")
		}
		return "/usr/bin/" + name, nil
	}
	c := one(t, doctor.Run(context.Background(), f.env), "nft", doctor.Blocker)
	eq(t, c.Fix, "sudo apt install nftables")
}

func TestOldKernel(t *testing.T) {
	f := newFake(t)
	f.root["proc/sys/kernel/osrelease"] = &fstest.MapFile{Data: []byte("5.3.0\n")}
	has(t, one(t, doctor.Run(context.Background(), f.env), "kernel", doctor.Blocker).Msg, "5.3.0")
}

func TestConflictProcess(t *testing.T) {
	for _, name := range []string{"nfqws", "nfqws2", "tpws", "ciadpi"} {
		f := newFake(t)
		f.root["opt/zapret2"] = &fstest.MapFile{Mode: os.ModeDir}
		f.env.Procs = func() ([]doctor.Proc, error) { return procs("bash", name), nil }
		c := one(t, doctor.Conflicts(context.Background(), f.env), "conflict", doctor.Blocker)
		has(t, c.Msg, name)
		if want := "sudo splitdpi takeover"; name == "ciadpi" && c.Fix == want || name != "ciadpi" && c.Fix != want {
			t.Errorf("%s: fix %q", name, c.Fix)
		}
	}
}

// splitdpi's own nfqws2 is not a conflict while the supervisor holds the lock.
func TestOwnChildNotConflict(t *testing.T) {
	f := newFake(t)
	holdLock(t, f.env.P, 4242)
	sup := os.Getpid()
	f.env.Procs = func() ([]doctor.Proc, error) {
		return []doctor.Proc{
			{PID: sup, PPID: 1, Comm: "splitdpi"},
			{PID: 4242, PPID: 1, Comm: "nfqws2"},   // recorded child, e.g. reparented
			{PID: 4300, PPID: sup, Comm: "nfqws2"}, // a child of the lock holder
			{PID: 4400, PPID: sup, Comm: "splitdpi"},
			{PID: 4401, PPID: 4400, Comm: "nfqws2"}, // under the guard: a grandchild
		}, nil
	}
	if cs := doctor.Conflicts(context.Background(), f.env); len(cs) != 0 {
		t.Errorf("own nfqws2 flagged: %+v", cs)
	}

	f.env.Procs = func() ([]doctor.Proc, error) {
		return []doctor.Proc{{PID: 4242, PPID: sup, Comm: "nfqws2"}, {PID: 1156, PPID: 1, Comm: "nfqws2"}}, nil
	}
	c := one(t, doctor.Conflicts(context.Background(), f.env), "conflict", doctor.Blocker)
	has(t, c.Msg, "nfqws2")
}

// blockcheck2 runs splitdpi's nfqws2 during a scan without the supervisor
// lock; started from the engine dir, it is still ours.
func TestEngineNfqws2NotConflict(t *testing.T) {
	f := newFake(t)
	ours := filepath.Join(f.env.P.EngineDir, "zapret2-v1.0.6/binaries/linux-x86_64/nfqws2")
	f.env.Procs = func() ([]doctor.Proc, error) {
		return []doctor.Proc{{PID: 700, PPID: 690, Comm: "nfqws2", Exe: ours}}, nil
	}
	if names, _ := doctor.ConflictProcs(f.env); len(names) != 0 {
		t.Errorf("engine nfqws2 flagged: %q", names)
	}
	f.env.Procs = func() ([]doctor.Proc, error) {
		return []doctor.Proc{
			{PID: 700, PPID: 690, Comm: "nfqws2", Exe: ours},
			{PID: 1156, PPID: 1, Comm: "nfqws2", Exe: "/opt/zapret2/binaries/linux-x86_64/nfqws2"},
		}, nil
	}
	c := one(t, doctor.Conflicts(context.Background(), f.env), "conflict", doctor.Blocker)
	has(t, c.Msg, "nfqws2")
}

func TestParseProcStat(t *testing.T) {
	p, ok := doctor.ParseProcStat("1156 (nfqws2) S 1 1156 1156 0 -1 4194560 100 0 0 0")
	if !ok || p != (doctor.Proc{PID: 1156, PPID: 1, Comm: "nfqws2"}) {
		t.Errorf("got %+v %v", p, ok)
	}
	p, ok = doctor.ParseProcStat("77 (a) b (c)) R 42 77 0")
	if !ok || p != (doctor.Proc{PID: 77, PPID: 42, Comm: "a) b (c)"}) {
		t.Errorf("comm with parens: %+v %v", p, ok)
	}
	if _, ok := doctor.ParseProcStat("garbage"); ok {
		t.Error("garbage parsed")
	}
}

// A stale status.json (supervisor gone, lock not held) must not hide a foreign nfqws2.
func TestStaleStatusStillConflicts(t *testing.T) {
	f := newFake(t)
	if err := os.MkdirAll(f.env.P.RunDir, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(f.env.P.LockFile, []byte(`{"pid":999999,"mode":"service"}`), 0o644)
	os.WriteFile(f.env.P.StatusFile, []byte(`{"state":"running","pid":999999,"child_pid":1156}`), 0o644)
	f.env.Procs = func() ([]doctor.Proc, error) { return []doctor.Proc{{PID: 1156, PPID: 999999, Comm: "nfqws2"}}, nil }
	one(t, doctor.Conflicts(context.Background(), f.env), "conflict", doctor.Blocker)
}

func TestConflictOptZapret(t *testing.T) {
	f := newFake(t)
	f.root["opt/zapret/uninstall_easy.sh"] = &fstest.MapFile{Mode: 0o755}
	c := one(t, doctor.Conflicts(context.Background(), f.env), "conflict", doctor.Warn)
	eq(t, c.Fix, "sudo /opt/zapret/uninstall_easy.sh")
}

func TestConflictOptZapret2(t *testing.T) {
	f := newFake(t)
	f.root["opt/zapret2/uninstall_easy.sh"] = &fstest.MapFile{Mode: 0o755}
	c := one(t, doctor.Conflicts(context.Background(), f.env), "conflict", doctor.Warn)
	eq(t, c.Fix, "sudo /opt/zapret2/uninstall_easy.sh")
}

// A distro package (e.g. AUR zapret2-bin) ships no uninstall script; don't suggest one.
func TestConflictOptZapret2Package(t *testing.T) {
	f := newFake(t)
	f.root["opt/zapret2/nfq2/nfqws2"] = &fstest.MapFile{Mode: 0o755}
	c := one(t, doctor.Conflicts(context.Background(), f.env), "conflict", doctor.Warn)
	eq(t, c.Fix, "")
	has(t, c.Msg, "/opt/zapret2")
}

func TestConflictImmutableResolvConf(t *testing.T) {
	f := newFake(t)
	f.out["lsattr /etc/resolv.conf"] = "----i---------e------- /etc/resolv.conf\n"
	c := one(t, doctor.Conflicts(context.Background(), f.env), "conflict", doctor.Warn)
	eq(t, c.Fix, "sudo chattr -i /etc/resolv.conf")
}

func TestNoConflicts(t *testing.T) {
	f := newFake(t)
	if cs := doctor.Conflicts(context.Background(), f.env); len(cs) != 0 {
		t.Errorf("got %+v, want none", cs)
	}
}

func TestNfqueueMissing(t *testing.T) {
	f := newFake(t)
	f.fail["nft -f"] = "Error: Could not process rule: No such file or directory"
	one(t, doctor.Run(context.Background(), f.env), "nfqueue", doctor.Blocker)
	assertProbeDeleted(t, f)
}

func TestNfqueueProbeDeleted(t *testing.T) {
	f := newFake(t)
	doctor.Run(context.Background(), f.env)
	assertProbeDeleted(t, f)
}

func TestNfqueueProbeDeletedOnCancel(t *testing.T) {
	f := newFake(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var gotCtxErr error
	inner := f.env.Run
	f.env.Run = func(c context.Context, stdin, name string, args ...string) ([]byte, error) {
		if name == "nft" && len(args) > 0 && args[0] == "delete" {
			gotCtxErr = c.Err()
		}
		return inner(c, stdin, name, args...)
	}
	doctor.Run(ctx, f.env)
	assertProbeDeleted(t, f)
	if gotCtxErr != nil {
		t.Errorf("probe delete ran with a cancelled context: %v", gotCtxErr)
	}
}

func assertProbeDeleted(t *testing.T, f *fake) {
	t.Helper()
	for _, c := range f.calls {
		if c == "nft delete table inet splitdpi_probe" {
			return
		}
	}
	t.Errorf("probe table not deleted; calls: %q", f.calls)
}

func TestSELinuxEnforcing(t *testing.T) {
	f := newFake(t)
	f.root["sys/fs/selinux/enforce"] = &fstest.MapFile{Data: []byte("1\n")}
	f.out["dmesg"] = strings.Join([]string{
		`audit: type=1400 avc:  denied  { execute } comm="nfqws2"`,
		`audit: type=1400 avc:  denied  { write } comm="splitdpi"`,
		`audit: type=1400 avc:  denied  { read } comm="firefox"`,
		`nfqws2 started`,
	}, "\n")
	msg := one(t, doctor.Run(context.Background(), f.env), "lsm", doctor.Warn).Msg
	has(t, msg, "2")
	has(t, msg, "dmesg")
}

func TestSELinuxAuditLogDenial(t *testing.T) {
	f := newFake(t)
	f.root["sys/fs/selinux/enforce"] = &fstest.MapFile{Data: []byte("1\n")}
	f.root["var/log/audit/audit.log"] = &fstest.MapFile{Data: []byte(strings.Join([]string{
		`type=AVC msg=audit(1761746000.123:410): avc:  denied  { write } for  pid=1217 comm="nfqws2" name="autohostlist.txt" scontext=system_u:system_r:unconfined_service_t:s0 tclass=file permissive=0`,
		`type=SYSCALL msg=audit(1761746000.123:410): arch=c000003e syscall=257 success=no exit=-13 comm="nfqws2"`,
		`type=AVC msg=audit(1761746001.000:411): avc:  granted  { setenforce } for  pid=900 comm="splitdpi"`,
	}, "\n"))}
	f.out["dmesg"] = `audit: type=1400 avc:  denied  { write } comm="splitdpi"` + "\n" + `audit: type=1400 avc:  denied  { read } comm="nfqws2"`
	msg := one(t, doctor.Run(context.Background(), f.env), "lsm", doctor.Warn).Msg
	has(t, msg, "1 denial for")
	has(t, msg, "/var/log/audit/audit.log")
}

func TestSELinuxAuditLogUnrelated(t *testing.T) {
	f := newFake(t)
	f.root["sys/fs/selinux/enforce"] = &fstest.MapFile{Data: []byte("1\n")}
	f.root["var/log/audit/audit.log"] = &fstest.MapFile{Data: []byte(
		`type=AVC msg=audit(1761746000.123:410): avc:  denied  { read } for  pid=2000 comm="firefox" permissive=0` + "\n")}
	f.out["dmesg"] = `audit: type=1400 avc:  denied  { write } comm="splitdpi"`
	msg := one(t, doctor.Run(context.Background(), f.env), "lsm", doctor.OK).Msg
	has(t, msg, "enforcing")
	has(t, msg, "0 denials")
}

func TestSELinuxNoLog(t *testing.T) {
	f := newFake(t)
	f.root["sys/fs/selinux/enforce"] = &fstest.MapFile{Data: []byte("1\n")}
	f.fail["dmesg"] = "operation not permitted"
	has(t, one(t, doctor.Run(context.Background(), f.env), "lsm", doctor.Warn).Msg, "operation not permitted")
}

func TestInitOther(t *testing.T) {
	f := newFake(t)
	delete(f.root, "run/systemd/system")
	has(t, one(t, doctor.Run(context.Background(), f.env), "init", doctor.Warn).Msg, "splitdpi run")
}

func TestEngineMissing(t *testing.T) {
	f := newFake(t)
	f.env.P = paths.Under(t.TempDir())
	c := one(t, doctor.Run(context.Background(), f.env), "engine", doctor.Blocker)
	eq(t, c.Fix, "sudo splitdpi engine install")
}

func TestEngineOld(t *testing.T) {
	f := newFake(t)
	f.env.P = paths.Under(t.TempDir())
	installEngine(t, f.env.P, "zapret2-v1.0.5")
	c := one(t, doctor.Run(context.Background(), f.env), "engine", doctor.Warn)
	eq(t, c.Fix, "sudo splitdpi engine install")
}

func TestExitCode(t *testing.T) {
	if doctor.ExitCode([]doctor.Check{{Severity: doctor.OK}, {Severity: doctor.Warn}}) != 0 {
		t.Error("warnings only: want 0")
	}
	if doctor.ExitCode([]doctor.Check{{Severity: doctor.OK}, {Severity: doctor.Blocker}}) != 1 {
		t.Error("with a blocker: want 1")
	}
}
