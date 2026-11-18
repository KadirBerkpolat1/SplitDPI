package scan_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/KadirBerkpolat1/SplitDPI/internal/engine"
	"github.com/KadirBerkpolat1/SplitDPI/internal/probe"
	"github.com/KadirBerkpolat1/SplitDPI/internal/scan"
)

// A result with no address to test neither fails a trial nor passes it alone.
func TestTrialSkipsNoAddress(t *testing.T) {
	for _, tc := range []struct {
		name    string
		results []probe.Result
		win     bool
	}{
		{"one opens", []probe.Result{{OK: true}, {NoAddress: true}}, true},
		{"none tested", []probe.Result{{NoAddress: true}, {NoAddress: true}}, false},
		{"one blocked", []probe.Result{{Err: "reset"}, {NoAddress: true}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env, _, _ := testEnv(t, testSet(), func(string) []probe.Result { return tc.results })
			rep, err := scan.Scan(context.Background(), env, scan.Options{Apps: []string{"roblox"}, NoBlockcheck: true})
			if tc.win && (err != nil || rep.Winner == nil) {
				t.Errorf("err %v, winner %+v; want a winner", err, rep.Winner)
			}
			if !tc.win && !errors.Is(err, scan.ErrNoStrategy) {
				t.Errorf("err %v, winner %+v; want ErrNoStrategy", err, rep.Winner)
			}
		})
	}
}

// Each URL is probed once per trial, in the order apps, then sites.
func TestScanProbesEachURLOnce(t *testing.T) {
	env, _, _ := testEnv(t, testSet(), nil)
	var got []string
	env.Probe = func(_ context.Context, urls []string) []probe.Result {
		got = slices.Clone(urls)
		rs := make([]probe.Result, len(urls))
		for i, u := range urls {
			rs[i] = probe.Result{URL: u, OK: true}
		}
		return rs
	}
	opt := scan.Options{Apps: []string{"roblox", "roblox"}, Sites: []string{"x.example", "www.roblox.com", "x.example"}, NoBlockcheck: true}
	if _, err := scan.Scan(context.Background(), env, opt); err != nil {
		t.Fatal(err)
	}
	if want := []string{"https://www.roblox.com/", "https://x.example/"}; !slices.Equal(got, want) {
		t.Errorf("probed %q, want %q", got, want)
	}
}

const blockcheckChildDir = "SPLITDPI_TEST_BLOCKCHECK_DIR"

// blockcheck2 must not outlive us: killed, we can no longer stop it, and it
// would go on loading its tables and nfqws2 as root.
func TestBlockcheckDiesWithUs(t *testing.T) {
	if dir := os.Getenv(blockcheckChildDir); dir != "" {
		none := func(context.Context, string, string, ...string) ([]byte, error) { return nil, nil }
		scan.RunBlockcheck(context.Background(), engine.Engine{Dir: dir, ArchDir: "linux-x86_64"}, none,
			scan.BlockcheckParams{Domains: []string{"example.com"}, IPVS: "4"}, func(string) {
				os.WriteFile(filepath.Join(dir, "started"), nil, 0o644)
			})
		os.Exit(0)
	}
	dir := t.TempDir()
	script := "echo $$ > \"$PWD/pid\"\ntrap 'touch \"$PWD/term\"; exit 1' TERM\necho started\n" +
		"i=0\nwhile [ $i -lt 200 ]; do sleep 0.05; i=$((i+1)); done\n"
	if err := os.WriteFile(filepath.Join(dir, "blockcheck2.sh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if b, err := os.ReadFile(filepath.Join(dir, "pid")); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil {
				syscall.Kill(pid, syscall.SIGKILL)
			}
		}
	})
	cmd := exec.Command(os.Args[0], "-test.run=^TestBlockcheckDiesWithUs$")
	cmd.Env = append(os.Environ(), blockcheckChildDir+"="+dir)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	if !appears(filepath.Join(dir, "started"), 5*time.Second) {
		cmd.Process.Kill()
		cmd.Wait()
		t.Fatal("blockcheck2 did not start")
	}
	cmd.Process.Kill()
	cmd.Wait()
	if !appears(filepath.Join(dir, "term"), 3*time.Second) {
		t.Error("blockcheck2 got no SIGTERM when splitdpi died")
	}
}

func appears(path string, within time.Duration) bool {
	for end := time.Now().Add(within); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
		if _, err := os.Stat(path); err == nil {
			return true
		}
	}
	return false
}

// A scan cancelled while a trial probes ends with ctx's error: the trial is
// not recorded as failed, no other trial starts, and blockcheck2 does not run.
func TestScanCancelledInTrial(t *testing.T) {
	for _, opt := range []scan.Options{
		{Apps: []string{"roblox"}},
		{Apps: []string{"roblox"}, NoBlockcheck: true},
		{Site: "www.roblox.com"},
	} {
		ctx, cancel := context.WithCancel(context.Background())
		env, trials, bc := testEnv(t, testSet(), nil)
		env.Probe = func(_ context.Context, urls []string) []probe.Result {
			cancel()
			return []probe.Result{{URL: urls[0], Err: context.Canceled.Error(), Cause: context.Canceled}}
		}
		rep, err := scan.Scan(ctx, env, opt)
		cancel()
		if !errors.Is(err, context.Canceled) || len(rep.Trials) != 0 || trials.Load() != 1 {
			t.Errorf("%+v: err %v, trials recorded %d, started %d; want context.Canceled, 0, 1", opt, err, len(rep.Trials), trials.Load())
		}
		want := int32(0)
		if opt.Site != "" {
			want = 1 // before the cancel, which comes in a verification trial
		}
		if bc.Load() != want {
			t.Errorf("%+v: blockcheck2 ran %d times, want %d", opt, bc.Load(), want)
		}
	}
}

// A cancelled scan starts no blockcheck2: nothing to stop or clean up after.
func TestRunBlockcheckCancelledStartsNothing(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "blockcheck2.sh"), []byte("touch \"$PWD/started\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var calls []string
	run := func(_ context.Context, _ string, name string, args ...string) ([]byte, error) {
		mu.Lock()
		defer mu.Unlock()
		calls = append(calls, strings.Join(append([]string{name}, args...), " "))
		return nil, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := scan.RunBlockcheck(ctx, engine.Engine{Dir: dir, ArchDir: "linux-x86_64"}, run,
		scan.BlockcheckParams{Domains: []string{"example.com"}, IPVS: "4"}, func(string) {})
	mu.Lock()
	defer mu.Unlock()
	if !errors.Is(err, context.Canceled) || len(calls) != 0 {
		t.Errorf("err %v, ran %q; want context.Canceled and nothing run", err, calls)
	}
	if appears(filepath.Join(dir, "started"), 100*time.Millisecond) {
		t.Error("blockcheck2.sh ran")
	}
}
