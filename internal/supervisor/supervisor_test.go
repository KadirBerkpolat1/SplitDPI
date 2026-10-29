package supervisor_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/KadirBerkpolat1/SplitDPI/internal/paths"
	"github.com/KadirBerkpolat1/SplitDPI/internal/render"
	"github.com/KadirBerkpolat1/SplitDPI/internal/supervisor"
)

type fakeChild struct {
	pid  int
	exit chan error
	mu   sync.Mutex
	sigs []os.Signal
}

func newChild(pid int, exitNow bool) *fakeChild {
	c := &fakeChild{pid: pid, exit: make(chan error, 1)}
	if exitNow {
		c.exit <- errors.New("exit status 1")
	}
	return c
}

func (c *fakeChild) Wait() error { return <-c.exit }
func (c *fakeChild) Pid() int    { return c.pid }
func (c *fakeChild) StderrTail() []string {
	return []string{"nfqws2: something broke"}
}
func (c *fakeChild) Signal(s os.Signal) error {
	c.mu.Lock()
	c.sigs = append(c.sigs, s)
	c.mu.Unlock()
	if s == syscall.SIGTERM {
		select {
		case c.exit <- nil:
		default:
		}
	}
	return nil
}
func (c *fakeChild) signals() []os.Signal {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.sigs)
}

type call struct{ cmd, stdin string }

type harness struct {
	s       *supervisor.Supervisor
	mu      sync.Mutex
	calls   []call
	sleeps  []time.Duration
	spawned chan *fakeChild
}

func newHarness(t *testing.T, p paths.Paths, exitNow bool) *harness {
	h := &harness{spawned: make(chan *fakeChild, 16)}
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	h.s = &supervisor.Supervisor{
		P: p,
		Exec: func(_ context.Context, stdin, name string, args ...string) ([]byte, error) {
			h.mu.Lock()
			defer h.mu.Unlock()
			h.calls = append(h.calls, call{strings.Join(append([]string{name}, args...), " "), stdin})
			return nil, nil
		},
		Spawn: func(argv []string) (supervisor.Child, error) {
			c := newChild(4000+len(h.spawned), exitNow)
			h.spawned <- c
			return c, nil
		},
		Now: func() time.Time { return now },
		Sleep: func(d time.Duration) {
			h.mu.Lock()
			h.sleeps = append(h.sleeps, d)
			h.mu.Unlock()
		},
		Log: os.Stderr,
		UID: os.Getuid(),
		GID: os.Getgid(),
	}
	return h
}

func (h *harness) cmds() []call {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.calls)
}

var out = render.Output{
	Argv:     []string{"/x/nfqws2", "--qnum=200"},
	Hostlist: []string{"discord.com", "discord.gg"},
	Exclude:  []string{"ziraat.com.tr"},
}

const rules = "table inet splitdpi {}\n"

func setup(t *testing.T) paths.Paths {
	p := paths.Under(t.TempDir())
	out.HostlistFile, out.ExcludeFile = p.HostlistFile, p.ExcludeFile
	return p
}

func meta() supervisor.Status { return supervisor.Status{Mode: "foreground", Strategy: "tt", Engine: "v1.0.6"} }

// The redirect in the table must never point at a forwarder that is not listening yet.
func TestStartsDNSBeforeTable(t *testing.T) {
	p := setup(t)
	h := newHarness(t, p, false)
	var dnsCtx context.Context
	h.s.StartDNS = func(ctx context.Context) error {
		dnsCtx = ctx
		h.mu.Lock()
		h.calls = append(h.calls, call{cmd: "dns start"})
		h.mu.Unlock()
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- h.s.Run(ctx, out, rules, meta()) }()
	<-h.spawned
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, c := range h.cmds() {
		order = append(order, c.cmd)
	}
	if dns, load := slices.Index(order, "dns start"), slices.Index(order, "nft -f -"); dns < 0 || load < 0 || dns > load {
		t.Errorf("order %q: forwarder must start before the table loads", order)
	}
	if dnsCtx.Err() == nil {
		t.Error("forwarder context still live after Run returned")
	}
}

func TestDNSStartFailureAborts(t *testing.T) {
	p := setup(t)
	h := newHarness(t, p, false)
	h.s.StartDNS = func(context.Context) error { return errors.New("bind 127.0.0.1:5453: address already in use") }
	err := h.s.Run(context.Background(), out, rules, meta())
	if err == nil || !strings.Contains(err.Error(), "5453") {
		t.Fatalf("got %v", err)
	}
	for _, c := range h.cmds() {
		if c.cmd == "nft -f -" {
			t.Error("table loaded although the forwarder did not start")
		}
	}
	if len(h.spawned) != 0 {
		t.Error("nfqws2 spawned although the forwarder did not start")
	}
	if st, _ := supervisor.ReadStatus(p); st.State != "failed" {
		t.Errorf("state %q", st.State)
	}
}

func TestCrashLoop(t *testing.T) {
	p := setup(t)
	h := newHarness(t, p, true)
	err := h.s.Run(context.Background(), out, rules, meta())
	if !errors.Is(err, supervisor.ErrCrashLoop) {
		t.Fatalf("got %v, want ErrCrashLoop", err)
	}
	if want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second}; !slices.Equal(h.sleeps, want) {
		t.Errorf("sleeps %v, want %v", h.sleeps, want)
	}
	if len(h.spawned) != 5 {
		t.Errorf("%d spawns, want 5", len(h.spawned))
	}
	calls := h.cmds()
	if last := calls[len(calls)-1].cmd; last != "nft delete table inet splitdpi" {
		t.Errorf("last nft call %q", last)
	}
	st, err := supervisor.ReadStatus(p)
	if err != nil || st.State != "failed" || st.LastError == "" || len(st.StderrTail) == 0 {
		t.Errorf("status %+v, %v", st, err)
	}
}

func TestCleanStop(t *testing.T) {
	p := setup(t)
	h := newHarness(t, p, false)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- h.s.Run(ctx, out, rules, meta()) }()
	c := <-h.spawned

	if b, _ := os.ReadFile(p.HostlistFile); string(b) != "discord.com\ndiscord.gg\n" {
		t.Errorf("hostlist file %q", b)
	}
	if b, _ := os.ReadFile(p.ExcludeFile); string(b) != "ziraat.com.tr\n" {
		t.Errorf("exclude file %q", b)
	}
	if _, err := os.Stat(p.AutoHostlist); err != nil {
		t.Errorf("autohostlist not created: %v", err)
	}
	// The status is written after Spawn returns, so give it a moment.
	var st supervisor.Status
	for range 100 {
		if st, _ = supervisor.ReadStatus(p); st.State == "running" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if st.State != "running" || st.ChildPID != c.pid || st.PID != os.Getpid() {
		t.Errorf("running status %+v", st)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run: %v", err)
	}
	if sigs := c.signals(); len(sigs) == 0 || sigs[0] != syscall.SIGTERM {
		t.Errorf("child signals %v", sigs)
	}
	calls := h.cmds()
	if last := calls[len(calls)-1].cmd; last != "nft delete table inet splitdpi" {
		t.Errorf("last nft call %q", last)
	}
	ents, _ := os.ReadDir(p.RunDir)
	if len(ents) != 1 || ents[0].Name() != "status.json" {
		t.Errorf("run dir left %v", ents)
	}
	if st, _ := supervisor.ReadStatus(p); st.State != "stopped" {
		t.Errorf("state %q", st.State)
	}
	if _, _, held := supervisor.LockInfo(p); held {
		t.Error("lock still held after stop")
	}
}

func TestAlreadyRunning(t *testing.T) {
	p := setup(t)
	h := newHarness(t, p, false)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- h.s.Run(ctx, out, rules, meta()) }()
	<-h.spawned

	if pid, mode, held := supervisor.LockInfo(p); !held || pid != os.Getpid() || mode != "foreground" {
		t.Errorf("LockInfo = %d %q %v", pid, mode, held)
	}
	second := newHarness(t, p, false)
	err := second.s.Run(context.Background(), out, rules, supervisor.Status{Mode: "service"})
	var already supervisor.ErrAlreadyRunning
	if !errors.As(err, &already) || already.Mode != "foreground" {
		t.Errorf("second Run: %v", err)
	}
	if len(second.cmds()) != 0 {
		t.Errorf("second Run touched nft: %v", second.cmds())
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

// Review Focus 1: kill -9 or power loss left a lock file with a dead PID and our table.
func TestRunWithStaleLockAndTable(t *testing.T) {
	p := setup(t)
	if err := os.MkdirAll(p.RunDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.LockFile, []byte(`{"pid":999999,"mode":"service"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if pid, _, held := supervisor.LockInfo(p); held || pid != 999999 {
		t.Errorf("stale LockInfo = %d %v", pid, held)
	}
	h := newHarness(t, p, false)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- h.s.Run(ctx, out, rules, meta()) }()
	<-h.spawned
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	calls := h.cmds()
	if len(calls) < 2 || calls[0].cmd != "nft delete table inet splitdpi" || calls[1].cmd != "nft -f -" || calls[1].stdin != rules {
		t.Errorf("calls %+v", calls)
	}
}

// The test binary doubles as the guard, like the real splitdpi binary does.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == supervisor.GuardArg {
		os.Exit(supervisor.Guard(os.Args[2:]))
	}
	os.Exit(m.Run())
}

func TestExecSpawnGuardPassesExitAndStderr(t *testing.T) {
	c, err := supervisor.ExecSpawn(os.Stderr, os.Args[0])([]string{"sh", "-c", "echo one >&2; exit 3"})
	if err != nil {
		t.Fatal(err)
	}
	var exit *exec.ExitError
	if err := c.Wait(); !errors.As(err, &exit) || exit.ExitCode() != 3 {
		t.Errorf("got %v, want exit 3", err)
	}
	if tail := c.StderrTail(); !slices.Equal(tail, []string{"one"}) {
		t.Errorf("tail %q", tail)
	}
}

func TestExecSpawnGuardForwardsSIGTERM(t *testing.T) {
	c, err := supervisor.ExecSpawn(os.Stderr, os.Args[0])([]string{"sleep", "60"})
	if err != nil {
		t.Fatal(err)
	}
	var sleepPID int
	deadline := time.Now().Add(2 * time.Second)
	for sleepPID == 0 && time.Now().Before(deadline) {
		// Go forks from any thread, so look at every task's children.
		tasks, _ := filepath.Glob(fmt.Sprintf("/proc/%d/task/*/children", c.Pid()))
		for _, f := range tasks {
			if b, _ := os.ReadFile(f); len(b) > 0 {
				fmt.Sscan(string(b), &sleepPID)
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if sleepPID == 0 {
		t.Fatal("guard did not start its child")
	}
	start := time.Now()
	c.Signal(syscall.SIGTERM)
	c.Wait()
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("stop took %s", d)
	}
	if err := syscall.Kill(sleepPID, 0); err == nil {
		t.Error("guarded child survived the guard")
	}
}

// fakeNft puts an `nft` on PATH that appends its arguments to the returned file.
func fakeNft(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "calls")
	script := "#!/bin/sh\necho \"$@\" >> " + log + "\n"
	if err := os.WriteFile(filepath.Join(dir, "nft"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	return log
}

func guardedSleep(t *testing.T, opts ...string) supervisor.Child {
	t.Helper()
	c, err := supervisor.ExecSpawn(os.Stderr, os.Args[0], opts...)([]string{"sleep", "60"})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond) // let the guard install its signal handler
	return c
}

// Nothing of ours may block traffic after a crash or kill -9. The
// guard outlives the supervisor (Pdeathsig SIGTERM) and drops the DNS redirect,
// whose forwarder died with the supervisor.
func TestGuardDropsChainOnSIGTERM(t *testing.T) {
	calls := fakeNft(t)
	c := guardedSleep(t, supervisor.DropChainOpt("splitdpi", "dns"))
	c.Signal(syscall.SIGTERM)
	c.Wait()
	b, err := os.ReadFile(calls)
	if err != nil || strings.TrimSpace(string(b)) != "delete chain inet splitdpi dns" {
		t.Errorf("nft calls %q, %v", b, err)
	}
}

func TestGuardWithoutChainLeavesNftAlone(t *testing.T) {
	calls := fakeNft(t)
	c := guardedSleep(t)
	c.Signal(syscall.SIGTERM)
	c.Wait()
	if b, err := os.ReadFile(calls); err == nil {
		t.Errorf("nft called without a chain to drop: %q", b)
	}
}

func TestExecSpawn(t *testing.T) {
	c, err := supervisor.ExecSpawn(os.Stderr, "")([]string{"sh", "-c", "echo one >&2; echo two >&2; exit 3"})
	if err != nil {
		t.Fatal(err)
	}
	if c.Pid() <= 0 {
		t.Errorf("pid %d", c.Pid())
	}
	if err := c.Wait(); err == nil {
		t.Error("exit 3 reported as success")
	}
	if tail := c.StderrTail(); !slices.Equal(tail, []string{"one", "two"}) {
		t.Errorf("tail %q", tail)
	}
}
