package presets

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/KadirBerkpolat1/SplitDPI/internal/paths"
	"github.com/KadirBerkpolat1/SplitDPI/internal/safetar"
)

const validApp = `schema = 1
id = "fresh"
name = "Fresh"
hosts = ["fresh.example"]
probe = ["https://fresh.example/"]
`

func presetTar(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{
			Name: name,
			Mode: 0o644,
			Size: int64(len(body)),
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// sumLine is the sha256sum line a release publishes for body.
func sumLine(body []byte) string {
	s := sha256.Sum256(body)
	return hex.EncodeToString(s[:]) + "  presets.tar.gz\n"
}

// serve answers each path in files with its body and any other with 404. It
// returns a client and the bundle URL to pass to Update.
func serve(t *testing.T, files map[string]string) (*http.Client, string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv.Client(), srv.URL + "/presets.tar.gz"
}

// release serves body as the bundle along with its checksum file.
func release(t *testing.T, body []byte) (*http.Client, string) {
	t.Helper()
	return serve(t, map[string]string{"/presets.tar.gz": string(body), "/presets.tar.gz.sha256": sumLine(body)})
}

func seedCache(t *testing.T, p paths.Paths) {
	t.Helper()
	dir := filepath.Join(p.PresetCache, "apps")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	old := `schema = 1
id = "old"
name = "Old"
hosts = ["old.example"]
probe = ["https://old.example/"]
`
	if err := os.WriteFile(filepath.Join(dir, "old.toml"), []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
}

// checkCache fails unless StateDir holds nothing but the seeded cache.
func checkCache(t *testing.T, p paths.Paths) {
	t.Helper()
	if Current(p).Apps["old"].Name != "Old" {
		t.Error("cache changed")
	}
	des, err := os.ReadDir(p.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, de := range des {
		names = append(names, de.Name())
	}
	if !slices.Equal(names, []string{"presets"}) {
		t.Errorf("state dir entries = %v", names)
	}
}

func TestUpdateValid(t *testing.T) {
	body := presetTar(t, map[string]string{
		"apps/fresh.toml": validApp,
		"VERSION":         "2099-01-01",
	})
	c, url := release(t, body)

	p := paths.Under(t.TempDir())
	rep, err := Update(context.Background(), c, p, url)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Apps) != 1 || rep.Apps["fresh"].Name != "Fresh" {
		t.Fatalf("set apps %+v", rep.Apps)
	}
	cur := Current(p)
	if cur.Origin != "cache" || cur.Apps["fresh"].Name != "Fresh" {
		t.Fatalf("current %+v", cur)
	}
}

func TestUpdateBadBundleKeepsCache(t *testing.T) {
	body := presetTar(t, map[string]string{
		"apps/bad.toml": "not valid toml [[",
	})
	c, url := release(t, body)

	p := paths.Under(t.TempDir())
	seedCache(t, p)
	_, err := Update(context.Background(), c, p, url)
	if !errors.Is(err, errNoApps) {
		t.Fatalf("err = %v", err)
	}
	cur := Current(p)
	if cur.Apps["old"].Name != "Old" {
		t.Fatalf("cache changed: %+v", cur.Apps)
	}
}

func TestUpdate404KeepsCache(t *testing.T) {
	c, url := serve(t, nil)

	p := paths.Under(t.TempDir())
	seedCache(t, p)
	_, err := Update(context.Background(), c, p, url)
	if err == nil {
		t.Fatal("want error")
	}
	if Current(p).Apps["old"].Name != "Old" {
		t.Fatal("cache changed")
	}
}

func TestUpdateChecksum(t *testing.T) {
	body := presetTar(t, map[string]string{"apps/fresh.toml": validApp})
	bundle, sum := string(body), sumLine(body)
	withSum := func(s string) map[string]string {
		return map[string]string{"/presets.tar.gz": bundle, "/presets.tar.gz.sha256": s}
	}
	cases := map[string]struct {
		files map[string]string
		want  string // in the error; "" for success
		is    error  // the checksum error it wraps, if any
	}{
		"match":            {withSum(sum), "", nil},
		"match no newline": {withSum(strings.TrimSuffix(sum, "\n")), "", nil},
		"mismatch":         {withSum(sumLine([]byte("other"))), "SHA-256 mismatch", ErrChecksumMismatch},
		"missing":          {map[string]string{"/presets.tar.gz": bundle}, "presets.tar.gz.sha256: HTTP 404", ErrNoChecksum},
		"empty":            {withSum(""), "is not one", ErrBadChecksum},
		"short digest":     {withSum(sum[1:]), "is not one", ErrBadChecksum},
		"not hex":          {withSum("g" + sum[1:]), "is not one", ErrBadChecksum},
		"other file":       {withSum(sum[:64] + "  other.tar.gz\n"), "is not one", ErrBadChecksum},
		"one space":        {withSum(sum[:64] + " presets.tar.gz\n"), "is not one", ErrBadChecksum},
		"trailing space":   {withSum(sum[:64] + "  presets.tar.gz "), "is not one", ErrBadChecksum},
		"two lines":        {withSum(sum + sum), "more than 81 bytes", ErrBadChecksum},
		"bundle missing":   {map[string]string{"/presets.tar.gz.sha256": sum}, "presets.tar.gz: HTTP 404", nil},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			p := paths.Under(t.TempDir())
			seedCache(t, p)
			c, url := serve(t, tc.files)
			_, err := Update(context.Background(), c, p, url)
			if tc.want == "" {
				if err != nil || Current(p).Apps["fresh"].Name != "Fresh" {
					t.Fatalf("err = %v, apps %v", err, Current(p).Apps)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
			for _, sentinel := range []error{ErrNoChecksum, ErrBadChecksum, ErrChecksumMismatch} {
				if errors.Is(err, sentinel) != (sentinel == tc.is) {
					t.Errorf("err = %v: wraps %v is %v", err, sentinel, errors.Is(err, sentinel))
				}
			}
			checkCache(t, p)
		})
	}
}

// Only a release that lacks the checksum file is ErrNoChecksum: a server
// that fails says nothing about the release.
func TestUpdateChecksumServerFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(srv.Close)
	p := paths.Under(t.TempDir())
	seedCache(t, p)
	_, err := Update(context.Background(), srv.Client(), p, srv.URL+"/presets.tar.gz")
	if err == nil || errors.Is(err, ErrNoChecksum) || !strings.Contains(err.Error(), "presets.tar.gz.sha256: HTTP 503") {
		t.Fatalf("err = %v", err)
	}
	checkCache(t, p)
}

func TestUpdateRejectsBadArchive(t *testing.T) {
	full := strings.Repeat("x", int(bundleLimits.File))
	cases := map[string]struct {
		files map[string]string
		want  error
	}{
		"file over limit":  {map[string]string{"apps/fresh.toml": validApp, "exclude/big.txt": full + "x"}, safetar.ErrTooLarge},
		"total over limit": {map[string]string{"apps/fresh.toml": validApp, "a": full, "b": full, "c": full, "d": full}, safetar.ErrTooLarge},
		"unsafe path":      {map[string]string{"apps/fresh.toml": validApp, "../evil": "x"}, safetar.ErrUnsafePath},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			p := paths.Under(t.TempDir())
			seedCache(t, p)
			c, url := release(t, presetTar(t, tc.files))
			if _, err := Update(context.Background(), c, p, url); !errors.Is(err, tc.want) {
				t.Fatalf("err = %v", err)
			}
			checkCache(t, p)
		})
	}
}

func TestUpdateRejectsOversizedDownload(t *testing.T) {
	c, url := release(t, make([]byte, bundleLimits.Total+1))
	p := paths.Under(t.TempDir())
	seedCache(t, p)
	if _, err := Update(context.Background(), c, p, url); err == nil || !strings.Contains(err.Error(), "more than") {
		t.Fatalf("err = %v", err)
	}
	checkCache(t, p)
}
