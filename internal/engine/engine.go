// Package engine installs and locates the pinned upstream zapret2 release.
package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/KadirBerkpolat1/SplitDPI/internal/paths"
	"github.com/KadirBerkpolat1/SplitDPI/internal/safetar"
)

const (
	Version = "v1.0.6"
	URL     = "https://github.com/bol-van/zapret2/releases/download/v1.0.6/zapret2-v1.0.6.tar.gz"
	SHA256  = "eb412aebc82cdef1f09b6aa9df45c8ff60fb88683b6663e6c4a9275d87ea2db4"
)

var (
	ErrChecksum   = errors.New("engine archive SHA-256 mismatch")
	ErrUnsafePath = safetar.ErrUnsafePath
	ErrArch       = errors.New("unsupported architecture")
)

// limits caps what the archive may unpack to. zapret2 v1.0.6 holds 306 files,
// 19.1 MB in total and 3.3 MB at most; keep both limits at least twice that.
var limits = safetar.Limits{File: 16 << 20, Total: 128 << 20}

// rename is os.Rename, replaced in tests to make the swap fail.
var rename = os.Rename

var archDirs = map[string]string{"amd64": "linux-x86_64", "arm64": "linux-arm64"}

// Engine is an unpacked release. ArchDir is the binaries/ subdirectory recorded at install.
type Engine struct {
	Dir     string
	ArchDir string
}

func (e Engine) Nfqws2() string      { return filepath.Join(e.Dir, "binaries", e.ArchDir, "nfqws2") }
func (e Engine) Mdig() string        { return filepath.Join(e.Dir, "binaries", e.ArchDir, "mdig") }
func (e Engine) Blockcheck2() string { return filepath.Join(e.Dir, "blockcheck2.sh") }
func (e Engine) LuaDir() string      { return filepath.Join(e.Dir, "lua") }

// Install verifies tarball against wantSHA256, unpacks it and swaps it into
// EngineDir. On any failure the previously installed engine is left as it was;
// if putting it back fails, the error says so.
func Install(p paths.Paths, tarball io.Reader, wantSHA256, goarch string) (Engine, error) {
	arch, ok := archDirs[goarch]
	if !ok {
		return Engine{}, fmt.Errorf("%w: %s", ErrArch, goarch)
	}
	if err := os.MkdirAll(p.EngineDir, 0o755); err != nil {
		return Engine{}, err
	}
	f, err := os.CreateTemp(p.StateDir, ".download-*")
	if err != nil {
		return Engine{}, err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(f, h), tarball); err != nil {
		return Engine{}, err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != wantSHA256 {
		return Engine{}, fmt.Errorf("%w: got %s, want %s", ErrChecksum, got, wantSHA256)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return Engine{}, err
	}

	tmp, err := os.MkdirTemp(p.EngineDir, ".tmp-")
	if err != nil {
		return Engine{}, err
	}
	defer os.RemoveAll(tmp)
	if err := safetar.Extract(f, tmp, limits); err != nil {
		return Engine{}, err
	}
	name := "zapret2-" + Version
	staged := filepath.Join(tmp, name)
	if _, err := os.Stat(filepath.Join(staged, "binaries", arch, "nfqws2")); err != nil {
		return Engine{}, fmt.Errorf("engine archive has no nfqws2 for %s: %w", arch, err)
	}

	e := Engine{Dir: filepath.Join(p.EngineDir, name), ArchDir: arch}
	old := filepath.Join(tmp, "old")
	hadOld := true
	if err := rename(e.Dir, old); errors.Is(err, fs.ErrNotExist) {
		hadOld = false
	} else if err != nil {
		return Engine{}, err
	}
	if err := rename(staged, e.Dir); err != nil {
		if hadOld {
			if rerr := rename(old, e.Dir); rerr != nil {
				err = fmt.Errorf("%w; restoring the previous engine failed too: %w", err, rerr)
			}
		}
		return Engine{}, err
	}
	cur := filepath.Join(p.EngineDir, "current")
	if err := os.WriteFile(cur+".new", []byte(name+"\n"+arch+"\n"), 0o644); err != nil {
		return Engine{}, err
	}
	if err := rename(cur+".new", cur); err != nil {
		return Engine{}, err
	}
	return e, nil
}

// Installed returns the engine recorded in EngineDir/current if its nfqws2 exists.
func Installed(p paths.Paths) (Engine, bool) {
	b, err := os.ReadFile(filepath.Join(p.EngineDir, "current"))
	if err != nil {
		return Engine{}, false
	}
	f := strings.Fields(string(b))
	if len(f) != 2 {
		return Engine{}, false
	}
	e := Engine{Dir: filepath.Join(p.EngineDir, f[0]), ArchDir: f[1]}
	if _, err := os.Stat(e.Nfqws2()); err != nil {
		return Engine{}, false
	}
	return e, true
}
