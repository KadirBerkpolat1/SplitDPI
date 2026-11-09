package cli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// toServer is a client that sends every request to srv instead.
func toServer(srv *httptest.Server) *http.Client {
	target, _ := url.Parse(srv.URL)
	return &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		r = r.Clone(r.Context())
		r.URL.Scheme, r.URL.Host = target.Scheme, target.Host
		return http.DefaultTransport.RoundTrip(r)
	})}
}

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
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".sha256") {
			fmt.Fprintf(w, "%x  presets.tar.gz\n", sha256.Sum256(buf.Bytes()))
			return
		}
		w.Write(buf.Bytes())
	}))
	defer srv.Close()

	o := rootOps(t)
	o.HTTP = toServer(srv)
	out, code := runOps(t, o, "presets", "update")
	eq(t, code, 0)
	has(t, out, "Fetching https://github.com/")
	has(t, out, "Updated preset cache to version 2099-01-01 (1 apps).")
	has(t, out, "  apps/bad.toml: ")
}

// A checksum file that fails the presets release is explained, with a
// failing exit code: why nothing was changed and what to do, not the error
// underneath.
func TestPresetsUpdateChecksumErrors(t *testing.T) {
	bundle := []byte("never unpacked: the checksum fails it first")
	for name, tc := range map[string]struct {
		sum  string // the checksum file, "" for none
		want string
	}{
		"missing":   {"", "The latest presets release has no checksum file (presets.tar.gz.sha256), so nothing was changed. Your current presets keep working"},
		"malformed": {"not a sha256sum line\n", "(presets.tar.gz.sha256) is not in the expected format, so nothing was changed. Your current presets keep working"},
		"mismatch":  {fmt.Sprintf("%x  presets.tar.gz\n", sha256.Sum256([]byte("other"))), "The downloaded presets did not match their checksum, so nothing was changed. Try again later"},
	} {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case !strings.HasSuffix(r.URL.Path, ".sha256"):
					w.Write(bundle)
				case tc.sum == "":
					http.NotFound(w, r)
				default:
					io.WriteString(w, tc.sum)
				}
			}))
			defer srv.Close()
			o := rootOps(t)
			o.HTTP = toServer(srv)
			out, code := runOps(t, o, "presets", "update")
			eq(t, code, 1)
			has(t, out, tc.want)
			if strings.Contains(out, "preset bundle") {
				t.Errorf("prints the error underneath:\n%s", out)
			}
		})
	}
}
