package presets_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/KadirBerkpolat1/SplitDPI/internal/paths"
	"github.com/KadirBerkpolat1/SplitDPI/internal/presets"
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

func TestUpdateValid(t *testing.T) {
	body := presetTar(t, map[string]string{
		"apps/fresh.toml": validApp,
		"VERSION":         "2099-01-01",
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write(body)
	}))
	defer srv.Close()

	p := paths.Under(t.TempDir())
	rep, err := presets.Update(context.Background(), srv.Client(), p, srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Apps) != 1 || rep.Apps["fresh"].Name != "Fresh" {
		t.Fatalf("set apps %+v", rep.Apps)
	}
	cur := presets.Current(p)
	if cur.Origin != "cache" || cur.Apps["fresh"].Name != "Fresh" {
		t.Fatalf("current %+v", cur)
	}
}

func TestUpdateBadBundleKeepsCache(t *testing.T) {
	body := presetTar(t, map[string]string{
		"apps/bad.toml": "not valid toml [[",
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write(body)
	}))
	defer srv.Close()

	p := paths.Under(t.TempDir())
	seedCache(t, p)
	_, err := presets.Update(context.Background(), srv.Client(), p, srv.URL)
	if err == nil {
		t.Fatal("want error")
	}
	cur := presets.Current(p)
	if cur.Apps["old"].Name != "Old" {
		t.Fatalf("cache changed: %+v", cur.Apps)
	}
}

func TestUpdate404KeepsCache(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	p := paths.Under(t.TempDir())
	seedCache(t, p)
	_, err := presets.Update(context.Background(), srv.Client(), p, srv.URL)
	if err == nil {
		t.Fatal("want error")
	}
	if presets.Current(p).Apps["old"].Name != "Old" {
		t.Fatal("cache changed")
	}
}
