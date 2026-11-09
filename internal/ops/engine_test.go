package ops

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"slices"
	"testing"

	"github.com/KadirBerkpolat1/SplitDPI/internal/engine"
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

// installPinned installs a release of the pinned engine that holds only nfqws2.
func installPinned(t *testing.T, o *Ops) {
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
	b := buf.Bytes()
	if _, err := o.installEngine(bytes.NewReader(b), fmt.Sprintf("%x", sha256.Sum256(b))); err != nil {
		t.Fatal(err)
	}
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
