// Package engine installs and locates the pinned upstream zapret2 release.
package engine

import (
	"archive/tar"
	"compress/gzip"
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
)

const (
	Version = "v1.0.6"
	URL     = "https://github.com/bol-van/zapret2/releases/download/v1.0.6/zapret2-v1.0.6.tar.gz"
	SHA256  = "eb412aebc82cdef1f09b6aa9df45c8ff60fb88683b6663e6c4a9275d87ea2db4"
)

var (
	ErrChecksum   = errors.New("engine archive SHA-256 mismatch")
	ErrUnsafePath = errors.New("unsafe path in engine archive")
	ErrArch       = errors.New("unsupported architecture")
)

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
// EngineDir. On any failure the previously installed engine is left as it was.
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
	if err := untar(f, tmp); err != nil {
		return Engine{}, err
	}
	name := "zapret2-" + Version
	staged := filepath.Join(tmp, name)
	if _, err := os.Stat(filepath.Join(staged, "binaries", arch, "nfqws2")); err != nil {
		return Engine{}, fmt.Errorf("engine archive has no nfqws2 for %s: %w", arch, err)
	}

	e := Engine{Dir: filepath.Join(p.EngineDir, name), ArchDir: arch}
	old := filepath.Join(tmp, "old")
	if err := os.Rename(e.Dir, old); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return Engine{}, err
	}
	if err := os.Rename(staged, e.Dir); err != nil {
		os.Rename(old, e.Dir)
		return Engine{}, err
	}
	cur := filepath.Join(p.EngineDir, "current")
	if err := os.WriteFile(cur+".new", []byte(name+"\n"+arch+"\n"), 0o644); err != nil {
		return Engine{}, err
	}
	if err := os.Rename(cur+".new", cur); err != nil {
		return Engine{}, err
	}
	return e, nil
}

// untar unpacks a .tar.gz into dir. Entries must be directories, regular files,
// or symlinks with a relative target that never climbs ("..").
func untar(r io.Reader, dir string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if !filepath.IsLocal(h.Name) {
			return fmt.Errorf("%w: %s", ErrUnsafePath, h.Name)
		}
		switch h.Typeflag {
		case tar.TypeDir:
			err = root.MkdirAll(h.Name, 0o755)
		case tar.TypeReg:
			err = writeFile(root, h.Name, tr, fs.FileMode(h.Mode)&0o777)
		case tar.TypeSymlink:
			if !filepath.IsLocal(h.Linkname) {
				return fmt.Errorf("%w: %s -> %s", ErrUnsafePath, h.Name, h.Linkname)
			}
			if err = root.MkdirAll(filepath.Dir(h.Name), 0o755); err == nil {
				err = root.Symlink(h.Linkname, h.Name)
			}
		default:
			return fmt.Errorf("%w: %s (type %c)", ErrUnsafePath, h.Name, h.Typeflag)
		}
		if err != nil {
			return err
		}
	}
}

func writeFile(root *os.Root, name string, r io.Reader, mode fs.FileMode) error {
	if err := root.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		return err
	}
	f, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		return err
	}
	return f.Close()
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
