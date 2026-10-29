package ops

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"testing/fstest"
	"time"

	"github.com/KadirBerkpolat1/SplitDPI/internal/config"
	"github.com/KadirBerkpolat1/SplitDPI/internal/i18n"
	"github.com/KadirBerkpolat1/SplitDPI/internal/paths"
	"github.com/KadirBerkpolat1/SplitDPI/internal/presets"
	"github.com/KadirBerkpolat1/SplitDPI/internal/scan"
	"github.com/KadirBerkpolat1/SplitDPI/internal/supervisor"
)

func TestMain(m *testing.M) {
	i18n.Init("en")
	// Tests run unprivileged: chown the autohostlist to ourselves, not to nobody.
	pickUser = func() (string, int, int) { return "nobody", os.Getuid(), os.Getgid() }
	os.Exit(m.Run())
}

type child struct {
	exit chan error
	mu   sync.Mutex
	sigs []os.Signal
}

func (c *child) Wait() error          { return <-c.exit }
func (c *child) Pid() int             { return 4242 }
func (c *child) StderrTail() []string { return nil }
func (c *child) Signal(s os.Signal) error {
	c.mu.Lock()
	c.sigs = append(c.sigs, s)
	c.mu.Unlock()
	select {
	case c.exit <- nil:
	default:
	}
	return nil
}

type nftState struct {
	mu    sync.Mutex
	table bool
}

func (n *nftState) run(_ context.Context, _ string, name string, args ...string) ([]byte, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	switch strings.Join(append([]string{name}, args...), " ") {
	case "nft -f -":
		n.table = true
	case "nft delete table inet splitdpi":
		if !n.table {
			return nil, errors.New("Error: No such file or directory")
		}
		n.table = false
	}
	return nil, nil
}

func (n *nftState) exists() bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.table
}

const queueBound = "  200  12345     0 2 65531     0     0        0  1\n"

func trialOps(t *testing.T, root fstest.MapFS) (*Ops, *nftState, *[]*child) {
	t.Helper()
	p := paths.Under(t.TempDir())
	bin := filepath.Join(p.EngineDir, "zapret2-v1.0.6", "binaries", "linux-x86_64")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(bin, "nfqws2"), nil, 0o755)
	os.WriteFile(filepath.Join(p.EngineDir, "current"), []byte("zapret2-v1.0.6\nlinux-x86_64\n"), 0o644)
	n := &nftState{}
	var children []*child
	var mu sync.Mutex
	o := &Ops{
		P: p, Exec: n.run, Root: root, Now: time.Now, Euid: func() int { return 0 }, GOARCH: "amd64",
		Spawn: func([]string) (supervisor.Child, error) {
			c := &child{exit: make(chan error, 1)}
			mu.Lock()
			children = append(children, c)
			mu.Unlock()
			return c, nil
		},
	}
	return o, n, &children
}

func trialConfig() config.Config {
	c := config.Default()
	c.Apps = []string{"discord"}
	return c
}

var candidate = presets.Strategy{ID: "tt", TCPTLS: "--payload=tls_client_hello --lua-desync=multidisorder:pos=2:seqovl=1"}

func TestTrialStartsAndTearsDown(t *testing.T) {
	o, n, children := trialOps(t, fstest.MapFS{"proc/net/netfilter/nfnetlink_queue": {Data: []byte(queueBound)}})
	stop, err := o.startTrial(context.Background(), trialConfig(), candidate, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !n.exists() || len(*children) != 1 {
		t.Fatalf("during trial: table %v, children %d", n.exists(), len(*children))
	}
	if _, mode, held := supervisor.LockInfo(o.P); !held || mode != "scan" {
		t.Errorf("trial lock %q %v", mode, held)
	}
	stop()
	if n.exists() {
		t.Error("table left after stop")
	}
	if c := (*children)[0]; !slices.Contains(c.sigs, os.Signal(syscall.SIGTERM)) {
		t.Errorf("child not stopped: %v", c.sigs)
	}
	if _, _, held := supervisor.LockInfo(o.P); held {
		t.Error("lock held after stop")
	}
}

// With fail-open queues, probing before nfqws2 binds the queue would test no bypass at all.
func TestTrialFailsIfQueueNeverBinds(t *testing.T) {
	old := trialReadyTimeout
	trialReadyTimeout = 200 * time.Millisecond
	t.Cleanup(func() { trialReadyTimeout = old })
	o, n, _ := trialOps(t, fstest.MapFS{})
	stop, err := o.startTrial(context.Background(), trialConfig(), candidate, nil)
	if err == nil {
		stop()
		t.Fatal("trial started although nfqws2 never bound the queue")
	}
	if n.exists() {
		t.Error("table left after failed trial")
	}
}

func TestPrepareDNS(t *testing.T) {
	o, _, _ := trialOps(t, fstest.MapFS{})
	cfg := trialConfig()
	cfg.Strategy = &config.Strategy{ID: "tt", TCPTLS: candidate.TCPTLS}
	j, err := o.prepare(cfg, "foreground", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if j.sup.StartDNS != nil || strings.Contains(j.ruleset, "redirect") {
		t.Error("DNS forwarder or redirect without dns = true")
	}
	cfg.DNSProvider = "auto"
	if j, err = o.prepare(cfg, "foreground", nil, nil); err != nil {
		t.Fatal(err)
	}
	if j.sup.StartDNS == nil || !strings.Contains(j.ruleset, "redirect to :5453") {
		t.Errorf("dns = true: StartDNS %v, ruleset:\n%s", j.sup.StartDNS != nil, j.ruleset)
	}
}

// A site being scanned is not a user site yet, but its trials must handle it.
func TestPrepareExtraHosts(t *testing.T) {
	o, _, _ := trialOps(t, fstest.MapFS{})
	cfg := trialConfig()
	cfg.Strategy = &config.Strategy{ID: "tt", TCPTLS: candidate.TCPTLS}
	j, err := o.prepare(cfg, "scan", nil, []string{"pastebin.com"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(j.out.Hostlist, "pastebin.com") {
		t.Errorf("hostlist %q", j.out.Hostlist)
	}
	if sites, _ := o.SitesList(); len(sites) != 0 {
		t.Errorf("trial host saved as a user site: %q", sites)
	}
}

func TestGuardOptions(t *testing.T) {
	p := paths.Under(t.TempDir())
	cfg := trialConfig()
	if opts := guardOptions(cfg, p); len(opts) != 0 {
		t.Errorf("dns off: %q", opts)
	}
	cfg.DNSProvider = "auto"
	if opts := guardOptions(cfg, p); !slices.Equal(opts, []string{supervisor.DropChainOpt("splitdpi", "dns")}) {
		t.Errorf("dns on: %q", opts)
	}
}

func TestScanRefusesConflict(t *testing.T) {
	o, _, _ := trialOps(t, fstest.MapFS{
		"proc/1156/stat": {Data: []byte("1156 (nfqws2) S 1 1156 1156 0 -1 4194560 0 0 0 0")},
	})
	_, err := o.Scan(context.Background(), scan.Options{Apps: []string{"discord"}}, nil)
	var conflict ConflictError
	if !errors.As(err, &conflict) || !strings.Contains(conflict.Checks[0].Msg, "nfqws2") {
		t.Fatalf("got %v, want ConflictError naming nfqws2", err)
	}
}
