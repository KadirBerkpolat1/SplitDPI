//go:build integration

package integration_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/KadirBerkpolat1/SplitDPI/internal/config"
	"github.com/KadirBerkpolat1/SplitDPI/internal/engine"
	"github.com/KadirBerkpolat1/SplitDPI/internal/execx"
	"github.com/KadirBerkpolat1/SplitDPI/internal/ops"
	"github.com/KadirBerkpolat1/SplitDPI/internal/paths"
	"github.com/KadirBerkpolat1/SplitDPI/internal/scan"
	"github.com/KadirBerkpolat1/SplitDPI/internal/supervisor"
)

// scanNS holds the nft table of the scan's trials, away from the host's
// tables and from the other tests' namespaces.
const scanNS = "sdpi-scan"

// okHTTP answers every probe with 200, so trials need no internet.
type okHTTP struct{}

func (okHTTP) RoundTrip(r *http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
}

func TestScanSimulate(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SPLITDPI_ROOT", root)
	t.Setenv("SPLITDPI_BLOCKCHECK_SIMULATE", "1")

	exec.Command("ip", "netns", "del", scanNS).Run() // leftover from an interrupted run
	if _, err := sh("ip", "netns", "add", scanNS); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { exec.Command("ip", "netns", "del", scanNS).Run() })

	p := paths.Under(root)
	// The fake nfqws2 stays up until the trial stops it; its unique sleep shows leftovers.
	sleepArg := fmt.Sprintf("600.%d", os.Getpid())
	installSimulateEngine(t, p, "#!/bin/sh\nexec sleep "+sleepArg+"\n")
	t.Cleanup(func() { exec.Command("pkill", "-f", "sleep "+sleepArg).Run() })

	inNS := func(ctx context.Context, stdin, name string, args ...string) ([]byte, error) {
		return execx.Real(ctx, stdin, "ip", append([]string{"netns", "exec", scanNS, name}, args...)...)
	}
	o := &ops.Ops{
		P: p,
		// No processes, so the host's own bypass tools do not block the scan;
		// the fake nfqws2 cannot bind queue 200, so the binding is faked here.
		Root:   fstest.MapFS{"proc/net/netfilter/nfnetlink_queue": {Data: []byte("  200  12345     0 2 65531     0     0        0  1\n")}},
		Exec:   inNS,
		HTTP:   &http.Client{Transport: okHTTP{}},
		Now:    time.Now,
		Euid:   func() int { return 0 },
		GOARCH: "amd64",
		Spawn:  supervisor.ExecSpawn(io.Discard, bin),
	}
	rep, err := o.Scan(context.Background(), scan.Options{
		BlockcheckOnly: true,
		Apps:           []string{"roblox"},
		ASN:            1,
		Country:        "XX",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Blockcheck == nil || len(rep.Blockcheck.TLSCandidates) == 0 {
		t.Fatalf("no blockcheck2 candidates: %+v", rep.Blockcheck)
	}
	first := rep.Blockcheck.TLSCandidates[0]
	if rep.Winner == nil || rep.Winner.Source != "blockcheck2" || rep.Winner.TCPTLS != first {
		t.Fatalf("winner %+v, want blockcheck2's first candidate %q", rep.Winner, first)
	}
	if len(rep.Trials) != 1 || !rep.Trials[0].OK {
		t.Fatalf("trials %+v, want one passing re-test", rep.Trials)
	}
	c, err := config.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.Strategy == nil || c.Strategy.TCPTLS != first {
		t.Fatalf("config strategy not saved: %+v", c.Strategy)
	}

	if _, err := sh("ip", "netns", "exec", scanNS, "nft", "list", "table", "inet", "splitdpi"); err == nil {
		t.Error("trial left the inet splitdpi table behind")
	}
	if out, err := exec.Command("pgrep", "-f", "sleep "+sleepArg).Output(); err == nil {
		t.Errorf("trial left the fake nfqws2 running: %s", out)
	}
	if _, _, held := supervisor.LockInfo(p); held {
		t.Error("trial left the run lock held")
	}
}

func installSimulateEngine(t *testing.T, p paths.Paths, nfqws2 string) {
	t.Helper()
	fixture, err := filepath.Abs(filepath.Join("..", "..", "internal", "scan", "testdata", "bc-simulate.log"))
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(p.EngineDir, "zapret2-"+engine.Version)
	bin := filepath.Join(dir, "binaries", "linux-x86_64")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "nfqws2"), []byte(nfqws2), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "mdig"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\ncat " + fixture + "\n"
	if err := os.WriteFile(filepath.Join(dir, "blockcheck2.sh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	cur := filepath.Join(p.EngineDir, "current")
	if err := os.WriteFile(cur, []byte("zapret2-"+engine.Version+"\nlinux-x86_64\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}
