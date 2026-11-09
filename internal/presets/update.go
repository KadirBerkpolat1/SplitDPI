package presets

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/KadirBerkpolat1/SplitDPI/internal/paths"
	"github.com/KadirBerkpolat1/SplitDPI/internal/safetar"
)

const UpdateURL = "https://github.com/KadirBerkpolat1/SplitDPI-presets/releases/latest/download/presets.tar.gz"

// bundleName is the file name the release's checksum line must carry.
const bundleName = "presets.tar.gz"

// sumLineLen is the length of a sha256sum line for the bundle, newline included.
const sumLineLen = int64(2*sha256.Size + len("  "+bundleName+"\n"))

// bundleLimits caps the unpacked bundle, and Total also the download. The
// 2026-10-31 bundle packs 36 files, 11.5 KB in total and 1.1 KB at most, into
// 5.3 KB.
var bundleLimits = safetar.Limits{File: 1 << 20, Total: 4 << 20}

var errNoApps = errors.New("preset bundle has no usable apps")

// Update downloads a preset tarball and the sha256sum file published next to
// it (url + ".sha256"), checks one against the other, validates the bundle,
// and swaps PresetCache. Nothing is written before the checksum matches.
func Update(ctx context.Context, c *http.Client, p paths.Paths, url string) (Set, error) {
	if c == nil {
		c = http.DefaultClient
	}
	want, err := fetchSum(ctx, c, url+".sha256")
	if err != nil {
		return Set{}, err
	}
	bundle, err := get(ctx, c, url, bundleLimits.Total)
	if err != nil {
		return Set{}, err
	}
	if got := sha256.Sum256(bundle); got != want {
		return Set{}, fmt.Errorf("preset bundle SHA-256 mismatch: got %x, want %x", got, want)
	}

	if err := os.MkdirAll(p.StateDir, 0o755); err != nil {
		return Set{}, err
	}
	tmp := filepath.Join(p.StateDir, ".presets-tmp")
	_ = os.RemoveAll(tmp)
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		return Set{}, err
	}
	if err := safetar.Extract(bytes.NewReader(bundle), tmp, bundleLimits); err != nil {
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

// fetchSum reads the bundle's SHA-256 from the sha256sum line at url.
func fetchSum(ctx context.Context, c *http.Client, url string) ([sha256.Size]byte, error) {
	var sum [sha256.Size]byte
	b, err := get(ctx, c, url, sumLineLen)
	if err != nil {
		return sum, fmt.Errorf("preset bundle checksum: %w", err)
	}
	digest, name, ok := strings.Cut(strings.TrimSuffix(string(b), "\n"), "  ")
	if ok && name == bundleName && len(digest) == hex.EncodedLen(sha256.Size) {
		if _, err := hex.Decode(sum[:], []byte(digest)); err == nil {
			return sum, nil
		}
	}
	return sum, fmt.Errorf("preset bundle checksum: %s is not one %q line", url, "<sha256>  "+bundleName)
}

// get returns the body of url, which must answer 200 with at most limit bytes.
func get(ctx context.Context, c *http.Client, url string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: HTTP %d", url, resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("GET %s: more than %d bytes", url, limit)
	}
	return b, nil
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
