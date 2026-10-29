package cli

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/KadirBerkpolat1/SplitDPI/internal/ops"
	"github.com/KadirBerkpolat1/SplitDPI/internal/paths"
)

// fakeNft tracks whether our table exists, like the kernel would.
type fakeNft struct {
	mu    sync.Mutex
	table bool
	calls []string
	hooks map[string]func() // run after the command is recorded, e.g. a service starting
	out   map[string]string
}

var errNoTable = errors.New("nft: exit status 1: Error: No such file or directory")

func (f *fakeNft) run(_ context.Context, _ string, name string, args ...string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	cmd := strings.Join(append([]string{name}, args...), " ")
	f.calls = append(f.calls, cmd)
	if h, ok := f.hooks[cmd]; ok {
		h()
	}
	if out, ok := f.out[cmd]; ok {
		return []byte(out), nil
	}
	switch cmd {
	case "nft -f -":
		f.table = true
	case "nft delete table inet splitdpi":
		if !f.table {
			return nil, errNoTable
		}
		f.table = false
	case "nft list table inet splitdpi":
		if !f.table {
			return nil, errNoTable
		}
	}
	return nil, nil
}

func (f *fakeNft) saw(cmd string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if c == cmd {
			return true
		}
	}
	return false
}

func runOpsFor(t *testing.T, root fstest.MapFS) (*ops.Ops, *fakeNft) {
	f := &fakeNft{}
	return &ops.Ops{
		P:      paths.Under(t.TempDir()),
		Exec:   f.run,
		Root:   root,
		Euid:   func() int { return 0 },
		Now:    time.Now,
		GOARCH: "amd64",
	}, f
}

func writeConfig(t *testing.T, p paths.Paths, s string) {
	t.Helper()
	if err := os.MkdirAll(p.ConfigDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.ConfigFile, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Review Focus 2: a hand-edited config.toml is refused with the file and field named.
func TestRunRejectsBadConfig(t *testing.T) {
	for _, tc := range []struct {
		name, config string
		want         []string
	}{
		{"broken toml", "lang = \n", []string{"config.toml", "line"}},
		{"unknown app", "apps = [\"foo\"]\n[strategy]\nid = \"x\"\ntcp_tls = \"--payload=tls_client_hello --lua-desync=multisplit:pos=2\"\n", []string{"config.toml", "foo"}},
		{"bad strategy", "[strategy]\nid = \"x\"\ntcp_tls = \"--lua-init=@x\"\n", []string{"config.toml", "tcp_tls"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o, f := runOpsFor(t, fstest.MapFS{})
			writeConfig(t, o.P, tc.config)
			out, code := runOps(t, o, "run")
			eq(t, code, 1)
			for _, w := range tc.want {
				has(t, out, w)
			}
			if f.saw("nft -f -") {
				t.Error("table loaded despite a bad config")
			}
		})
	}
}

func TestRunRefusesConflict(t *testing.T) {
	o, f := runOpsFor(t, fstest.MapFS{
		"proc/1156/stat": {Data: []byte("1156 (nfqws2) S 1 1156 1156 0 -1 4194560 0 0 0 0")},
	})
	out, code := runOps(t, o, "run")
	eq(t, code, 1)
	has(t, out, "nfqws2")
	if len(f.calls) > 0 && f.saw("nft -f -") {
		t.Error("table loaded despite a conflict")
	}
}

func TestRunNoStrategy(t *testing.T) {
	o, _ := runOpsFor(t, fstest.MapFS{})
	out, code := runOps(t, o, "run")
	eq(t, code, 1)
	has(t, out, "splitdpi scan")
}

// Review Focus 2: revert never parses config, so garbage there cannot block it.
func TestRevertWithCorruptConfig(t *testing.T) {
	o, f := runOpsFor(t, fstest.MapFS{})
	f.table = true
	writeConfig(t, o.P, "\x00\x01 not toml [[[")
	if err := os.MkdirAll(o.P.RunDir, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(o.P.HostlistFile, []byte("discord.com\n"), 0o644)
	out, code := runOps(t, o, "revert")
	eq(t, code, 0)
	if !f.saw("nft delete table inet splitdpi") {
		t.Errorf("table not deleted; calls %q", f.calls)
	}
	if _, err := os.Stat(o.P.RunDir); !os.IsNotExist(err) {
		t.Errorf("run dir still there: %v", err)
	}
	has(t, out, "clean")
	if _, err := os.Stat(o.P.ConfigFile); err != nil {
		t.Errorf("revert without --purge removed config: %v", err)
	}
}

func TestRevertPurge(t *testing.T) {
	o, _ := runOpsFor(t, fstest.MapFS{})
	writeConfig(t, o.P, "mode = \"apps\"\n")
	if err := os.MkdirAll(o.P.EngineDir, 0o755); err != nil {
		t.Fatal(err)
	}
	out, code := runOps(t, o, "revert", "--purge")
	eq(t, code, 0)
	for _, d := range []string{o.P.ConfigDir, o.P.StateDir} {
		if _, err := os.Stat(d); !os.IsNotExist(err) {
			t.Errorf("%s still there: %v", d, err)
		}
	}
	has(t, out, "clean")
}

func TestRevertEmptyRootClean(t *testing.T) {
	o, _ := runOpsFor(t, fstest.MapFS{})
	out, code := runOps(t, o, "revert")
	eq(t, code, 0)
	has(t, out, "clean")
}

func TestStatusNotRunning(t *testing.T) {
	o, _ := runOpsFor(t, fstest.MapFS{})
	out, code := runOps(t, o, "status")
	eq(t, code, 0)
	has(t, out, "stopped")
	has(t, out, "splitdpi scan")
}

func TestStopNotRunning(t *testing.T) {
	o, _ := runOpsFor(t, fstest.MapFS{})
	out, code := runOps(t, o, "stop")
	eq(t, code, 0)
	has(t, out, "not running")
}

func TestRunCommandsUsage(t *testing.T) {
	for _, args := range [][]string{{"run", "x"}, {"run", "--background", "x"}, {"stop", "x"}, {"status", "x"}, {"revert", "--bogus"}} {
		if _, code := run(t, 0, args...); code != 2 {
			t.Errorf("%v: code %d, want 2", args, code)
		}
	}
}
