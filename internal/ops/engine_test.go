package ops

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/KadirBerkpolat1/SplitDPI/internal/engine"
	"github.com/KadirBerkpolat1/SplitDPI/internal/i18n"
	"github.com/KadirBerkpolat1/SplitDPI/internal/paths"
)

// installOps has v1.0.6 installed and v1.0.5 left behind by an earlier install.
func installOps(t *testing.T) *Ops {
	t.Helper()
	o := &Ops{P: paths.Under(t.TempDir()), GOARCH: "amd64"}
	installedEngine(t, o.P, "zapret2-v1.0.5")
	installedEngine(t, o.P, "zapret2-v1.0.6")
	return o
}

// pinnedRelease is a release of the pinned engine that holds only nfqws2, and its SHA-256.
func pinnedRelease(t *testing.T) ([]byte, string) {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	name := "zapret2-" + engine.Version + "/binaries/linux-x86_64/nfqws2"
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: 3}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte("elf")); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes(), fmt.Sprintf("%x", sha256.Sum256(buf.Bytes()))
}

// installPinned installs pinnedRelease.
func installPinned(t *testing.T, o *Ops) engine.Engine {
	t.Helper()
	b, sum := pinnedRelease(t)
	e, err := o.installEngine(bytes.NewReader(b), sum)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func sortedNames(s ...string) []string {
	slices.Sort(s)
	return s
}

// With no instance running, nothing uses the engines current no longer names.
func TestEngineInstallRemovesOldEngines(t *testing.T) {
	o := installOps(t)
	installPinned(t, o)
	if got, want := dirNames(o.P.EngineDir), sortedNames("current", "zapret2-"+engine.Version); !slices.Equal(got, want) {
		t.Errorf("engine dir holds %q, want %q", got, want)
	}
}

// A running instance may still run the engine an install replaces, so the old
// engines stay until the next run starts.
func TestEngineInstallKeepsOldEnginesWhileRunning(t *testing.T) {
	o := installOps(t)
	holdBackgroundLock(t, o.P)
	installPinned(t, o)
	want := sortedNames("current", "zapret2-v1.0.5", "zapret2-v1.0.6", "zapret2-"+engine.Version)
	if got := dirNames(o.P.EngineDir); !slices.Equal(got, want) {
		t.Errorf("engine dir holds %q, want %q", got, want)
	}
	if e, ok := engine.Installed(o.P); !ok || filepath.Base(e.Dir) != "zapret2-"+engine.Version {
		t.Errorf("installed %+v, %v; want the pinned engine", e, ok)
	}
}

// A scan holds the engine lock while blockcheck2 starts nfqws2 from the engine
// current named when it began, here v1.0.5: neither an install's cleanup nor
// a run's removes that engine before the scan ends. The next one after does.
func TestEngineCleanupSkipsWhileScanning(t *testing.T) {
	o, _, _ := engineOps(t, fstest.MapFS{}, "zapret2-v1.0.5")
	release, err := engine.LockShared(o.P)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	installPinned(t, o)
	both := sortedNames("current", "zapret2-v1.0.5", "zapret2-"+engine.Version)
	if got := dirNames(o.P.EngineDir); !slices.Equal(got, both) {
		t.Errorf("after install, engine dir holds %q, want %q", got, both)
	}
	if _, got := startRun(t, o, engine.Version); !slices.Equal(got, both) {
		t.Errorf("engine dir holds %q when nfqws2 starts, want %q", got, both)
	}
	release()
	want := sortedNames("current", "zapret2-"+engine.Version)
	if _, got := startRun(t, o, engine.Version); !slices.Equal(got, want) {
		t.Errorf("after the scan, engine dir holds %q when nfqws2 starts, want %q", got, want)
	}
}

// A failed cleanup only leaves disk space taken: install and run go on, and
// the log says what is left.
func TestEngineCleanupFailureIsLogged(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	o, _, _ := engineOps(t, fstest.MapFS{}, "zapret2-v1.0.6")
	lua := filepath.Join(o.P.EngineDir, "zapret2-v1.0.5", "lua")
	if err := os.MkdirAll(lua, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lua, "zapret-lib.lua"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(lua, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(lua, 0o755) })
	var log bytes.Buffer
	o.Log = &log
	failed := i18n.T("engine.remove_old_failed", "")
	e := installPinned(t, o)
	if want := filepath.Join(o.P.EngineDir, "zapret2-"+engine.Version); e.Dir != want {
		t.Errorf("installed %+v, want the engine in %s", e, want)
	}
	if n := strings.Count(log.String(), failed); n != 1 || !strings.Contains(log.String(), lua) {
		t.Errorf("after install, log %q has %d failures, want 1 naming %s", log.String(), n, lua)
	}
	if argv, _ := startRun(t, o, engine.Version); argv[0] != e.Nfqws2() {
		t.Errorf("run started %q, want %s", argv, e.Nfqws2())
	}
	if n := strings.Count(log.String(), failed); n != 2 {
		t.Errorf("after run, log %q has %d failures, want 2", log.String(), n)
	}
}
