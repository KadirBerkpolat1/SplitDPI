package scan_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/KadirBerkpolat1/SplitDPI/internal/domain"
	"github.com/KadirBerkpolat1/SplitDPI/internal/engine"
	"github.com/KadirBerkpolat1/SplitDPI/internal/execx"
	"github.com/KadirBerkpolat1/SplitDPI/internal/scan"
)

func TestBlockcheckEnv(t *testing.T) {
	env, err := scan.BlockcheckEnv(scan.BlockcheckParams{
		Domains: []string{"discord.com", "www.roblox.com"},
		IPVS:    "4",
		HTTP3:   false,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"BATCH":              "1",
		"SCANLEVEL":          "quick",
		"DOMAINS":            "discord.com www.roblox.com",
		"IPVS":               "4",
		"ENABLE_HTTP":        "0",
		"ENABLE_HTTPS_TLS12": "1",
		"ENABLE_HTTPS_TLS13": "0",
		"ENABLE_HTTP3":       "0",
	}
	got := envMap(t, env)
	if len(got) != len(want) {
		t.Fatalf("got %d vars %v, want %d", len(got), got, len(want))
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: got %q want %q", k, got[k], v)
		}
	}
}

func TestBlockcheckEnvDoH(t *testing.T) {
	env, err := scan.BlockcheckEnv(scan.BlockcheckParams{
		DoH: []string{"https://1.1.1.1/dns-query", "https://1.0.0.1/dns-query"},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := envMap(t, env)
	if got["SECURE_DNS"] != "1" || got["DOH_SERVERS"] != "https://1.1.1.1/dns-query https://1.0.0.1/dns-query" {
		t.Errorf("env %v", got)
	}
}

func TestBlockcheckEnvRejectsUnsafeDomain(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "started")
	if err := os.WriteFile(filepath.Join(dir, "blockcheck2.sh"), []byte("touch \""+marker+"\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"x.$(reboot).com", "a;b.com", "a'b.com", "a b.com", "localhost"} {
		p := scan.BlockcheckParams{Domains: []string{"discord.com", bad}, IPVS: "4"}
		env, err := scan.BlockcheckEnv(p)
		var inv domain.InvalidError
		if !errors.As(err, &inv) || inv.Input != bad {
			t.Errorf("BlockcheckEnv(%q): err %v, want InvalidError naming it", bad, err)
		}
		if env != nil {
			t.Errorf("BlockcheckEnv(%q): env %q, want none", bad, env)
		}
		if _, err := scan.RunBlockcheck(context.Background(), engine.Engine{Dir: dir, ArchDir: "linux-x86_64"}, (&nftRecorder{}).run, p, nil); !errors.As(err, &inv) {
			t.Errorf("RunBlockcheck(%q): err %v, want InvalidError", bad, err)
		}
		if _, err := os.Stat(marker); err == nil {
			t.Fatalf("RunBlockcheck(%q) started blockcheck2", bad)
		}
	}
}

func envMap(t *testing.T, env []string) map[string]string {
	t.Helper()
	m := make(map[string]string, len(env))
	for _, e := range env {
		k, v, ok := strings.Cut(e, "=")
		if !ok {
			t.Fatalf("bad env entry %q", e)
		}
		m[k] = v
	}
	return m
}

type nftRecorder struct {
	mu    sync.Mutex
	calls []string
}

func (r *nftRecorder) run(ctx context.Context, stdin string, name string, args ...string) ([]byte, error) {
	if name != "nft" {
		return nil, nil
	}
	r.mu.Lock()
	r.calls = append(r.calls, strings.Join(append([]string{name}, args...), " "))
	r.mu.Unlock()
	return nil, nil
}

func (r *nftRecorder) sorted() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := append([]string(nil), r.calls...)
	sort.Strings(out)
	return out
}

func blockcheckDeletePID(calls []string) (int, bool) {
	var inetPID, ipPID int
	for _, c := range calls {
		switch {
		case strings.HasPrefix(c, "nft delete table inet blockcheck2-"):
			n, err := strconv.Atoi(strings.TrimPrefix(c, "nft delete table inet blockcheck2-"))
			if err == nil {
				inetPID = n
			}
		case strings.HasPrefix(c, "nft delete table ip blockcheck2-"):
			rest := strings.TrimPrefix(c, "nft delete table ip blockcheck2-")
			rest = strings.TrimSuffix(rest, "_test")
			n, err := strconv.Atoi(rest)
			if err == nil {
				ipPID = n
			}
		}
	}
	if inetPID > 0 && inetPID == ipPID {
		return inetPID, true
	}
	return 0, false
}

func TestRunBlockcheckKeepsStdoutAndStderr(t *testing.T) {
	dir := t.TempDir()
	script := "i=0\nwhile [ $i -lt 2000 ]; do echo out$i; echo err$i >&2; i=$((i+1)); done\n"
	if err := os.WriteFile(filepath.Join(dir, "blockcheck2.sh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	rec := &nftRecorder{}
	lines := 0
	log, err := scan.RunBlockcheck(context.Background(), engine.Engine{Dir: dir, ArchDir: "linux-x86_64"},
		rec.run, scan.BlockcheckParams{Domains: []string{"example.com"}, IPVS: "4"}, func(string) { lines++ })
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(log, "\n"); got != 4000 || lines != 4000 {
		t.Fatalf("log has %d lines, progress saw %d; want 4000 each", got, lines)
	}
}

func TestRunBlockcheckCancel(t *testing.T) {
	src, err := os.ReadFile("testdata/fake-engine/blockcheck2.sh")
	if err != nil {
		t.Fatal(err)
	}
	// The fake engine keeps its mark files in its own directory.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "blockcheck2.sh"), src, 0o755); err != nil {
		t.Fatal(err)
	}
	mark := filepath.Join(dir, "mark")
	e := engine.Engine{Dir: dir, ArchDir: "linux-x86_64"}

	rec := &nftRecorder{}
	run := execx.Runner(rec.run)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var lines int
	var mu sync.Mutex
	progress := func(line string) {
		mu.Lock()
		lines++
		if lines == 1 {
			cancel()
			// SIGINT does not always reach the shell under go test; the
			// cancel file ends the fake engine then.
			_ = os.WriteFile(mark+".cancel", nil, 0o644)
		}
		mu.Unlock()
		_ = line
	}

	start := time.Now()
	log, runErr := scan.RunBlockcheck(ctx, e, run, scan.BlockcheckParams{
		Domains: []string{"discord.com"},
		IPVS:    "4",
	}, progress)
	elapsed := time.Since(start)

	if lines < 1 {
		t.Fatalf("progress lines %d", lines)
	}
	if elapsed > 16*time.Second {
		t.Fatalf("took %v, want cancel within 16s", elapsed)
	}
	if _, err := os.Stat(mark); err != nil {
		t.Fatalf("mark file: %v (log %q err %v)", err, log, runErr)
	}

	pid, ok := blockcheckDeletePID(rec.sorted())
	if !ok {
		t.Fatalf("want inet+ip blockcheck2 deletes, got %v", rec.sorted())
	}
	for _, want := range scan.BlockcheckNFTables(pid) {
		family, name, _ := strings.Cut(want, " ")
		needle := "nft delete table " + family + " " + name
		found := false
		for _, c := range rec.sorted() {
			if c == needle {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("missing %q in %v", needle, rec.sorted())
		}
	}
}
