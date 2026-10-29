package presets

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"

	"github.com/KadirBerkpolat1/SplitDPI/internal/paths"
)

const UpdateURL = "https://github.com/KadirBerkpolat1/SplitDPI-presets/releases/latest/download/presets.tar.gz"

var errNoApps = errors.New("preset bundle has no usable apps")

// Update downloads a preset tarball, validates it, and swaps PresetCache.
func Update(ctx context.Context, c *http.Client, p paths.Paths, url string) (Set, error) {
	if c == nil {
		c = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Set{}, err
	}
	resp, err := c.Do(req)
	if err != nil {
		return Set{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Set{}, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	if err := os.MkdirAll(p.StateDir, 0o755); err != nil {
		return Set{}, err
	}
	tmp := filepath.Join(p.StateDir, ".presets-tmp")
	_ = os.RemoveAll(tmp)
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		return Set{}, err
	}
	if err := untarPresets(resp.Body, tmp); err != nil {
		_ = os.RemoveAll(tmp)
		return Set{}, err
	}
	set := Load(os.DirFS(tmp))
	if len(set.Apps) == 0 {
		_ = os.RemoveAll(tmp)
		return Set{}, errNoApps
	}
	if err := swapPresetCache(p, tmp); err != nil {
		_ = os.RemoveAll(tmp)
		return Set{}, err
	}
	set.Origin = "cache"
	return set, nil
}

func swapPresetCache(p paths.Paths, tmp string) error {
	old := filepath.Join(p.StateDir, ".presets-old")
	_ = os.RemoveAll(old)
	if _, err := os.Stat(p.PresetCache); err == nil {
		if err := os.Rename(p.PresetCache, old); err != nil {
			return err
		}
	}
	if err := os.Rename(tmp, p.PresetCache); err != nil {
		_ = os.Rename(old, p.PresetCache)
		return err
	}
	_ = os.RemoveAll(old)
	return nil
}

func untarPresets(r io.Reader, dir string) error {
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
			return fmt.Errorf("unsafe path %q", h.Name)
		}
		switch h.Typeflag {
		case tar.TypeDir:
			err = root.MkdirAll(h.Name, 0o755)
		case tar.TypeReg:
			err = writePresetFile(root, h.Name, tr, fs.FileMode(h.Mode)&0o777)
		case tar.TypeSymlink:
			if !filepath.IsLocal(h.Linkname) {
				return fmt.Errorf("unsafe symlink %q", h.Name)
			}
			if err = root.MkdirAll(filepath.Dir(h.Name), 0o755); err == nil {
				err = root.Symlink(h.Linkname, h.Name)
			}
		default:
			return fmt.Errorf("unsupported tar entry %q", h.Name)
		}
		if err != nil {
			return err
		}
	}
}

func writePresetFile(root *os.Root, name string, r io.Reader, mode fs.FileMode) error {
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
