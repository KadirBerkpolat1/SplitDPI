package cli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestPresetsUpdatePrintsResult(t *testing.T) {
	files := map[string]string{
		"VERSION":         "2099-01-01",
		"apps/fresh.toml": "schema = 1\nid = \"fresh\"\nname = \"Fresh\"\nhosts = [\"fresh.example\"]\nprobe = [\"https://fresh.example/\"]\n",
		"apps/bad.toml":   "not toml [[",
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		tw.Write([]byte(body))
	}
	tw.Close()
	gz.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Write(buf.Bytes()) }))
	defer srv.Close()
	target, _ := url.Parse(srv.URL)

	o := rootOps(t)
	o.HTTP = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		r = r.Clone(r.Context())
		r.URL.Scheme, r.URL.Host = target.Scheme, target.Host
		return http.DefaultTransport.RoundTrip(r)
	})}
	out, code := runOps(t, o, "presets", "update")
	eq(t, code, 0)
	has(t, out, "Fetching https://github.com/")
	has(t, out, "Updated preset cache to version 2099-01-01 (1 apps).")
	has(t, out, "  apps/bad.toml: ")
}
