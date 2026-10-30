package ops

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/KadirBerkpolat1/SplitDPI/internal/config"
	"github.com/KadirBerkpolat1/SplitDPI/internal/paths"
	"github.com/KadirBerkpolat1/SplitDPI/internal/presets"
)

func loadConfig(t *testing.T, o *Ops) config.Config {
	t.Helper()
	c, err := config.Load(o.P)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// rejects checks that err is a translated message naming each of want.
func rejects(t *testing.T, err error, want ...string) {
	t.Helper()
	if err == nil {
		t.Fatal("accepted")
	}
	if strings.HasPrefix(err.Error(), "!") {
		t.Errorf("untranslated: %v", err)
	}
	for _, w := range want {
		if !strings.Contains(err.Error(), w) {
			t.Errorf("%q does not name %q", err, w)
		}
	}
}

func TestSetMode(t *testing.T) {
	o := &Ops{P: paths.Under(t.TempDir())}
	rejects(t, o.SetMode("all"), `"all"`, "broad", "apps")

	if err := o.SetMode(config.ModeApps); !errors.Is(err, ErrAppsModeEmpty) {
		t.Fatalf("apps with nothing enabled: %v", err)
	}
	if _, err := os.Stat(o.P.ConfigFile); err == nil {
		t.Error("refused mode still saved the config")
	}

	if _, err := o.SitesAdd("example.com"); err != nil {
		t.Fatal(err)
	}
	if err := o.SetMode(config.ModeApps); err != nil {
		t.Fatal(err)
	}
	if c := loadConfig(t, o); c.Mode != config.ModeApps {
		t.Errorf("mode %q", c.Mode)
	}
	if err := o.SetMode(config.ModeBroad); err != nil {
		t.Fatal(err)
	}
	if c := loadConfig(t, o); c.Mode != config.ModeBroad {
		t.Errorf("mode %q", c.Mode)
	}
}

func TestSetModeAppsWithAnApp(t *testing.T) {
	o := &Ops{P: paths.Under(t.TempDir())}
	if err := o.AppsEnable("discord"); err != nil {
		t.Fatal(err)
	}
	if err := o.SetMode(config.ModeApps); err != nil {
		t.Fatal(err)
	}
	if c := loadConfig(t, o); c.Mode != config.ModeApps {
		t.Errorf("mode %q", c.Mode)
	}
}

func TestSetSkipIfaces(t *testing.T) {
	o := &Ops{P: paths.Under(t.TempDir())}
	if err := o.SetSkipIfaces([]string{"wg0", "tun*", "eth0:1", "wg0", "br-lan.10"}); err != nil {
		t.Fatal(err)
	}
	if got, want := loadConfig(t, o).SkipIfaces, []string{"wg0", "tun*", "eth0:1", "br-lan.10"}; !slices.Equal(got, want) {
		t.Errorf("saved %q, want %q", got, want)
	}
	for _, bad := range []string{"", "*", "a*b", "sixteen-chars-xx", "wg 0", "wg\"0", "ağ0", "wg**"} {
		rejects(t, o.SetSkipIfaces([]string{"wg0", bad}), "1-15")
	}
	if got := loadConfig(t, o).SkipIfaces; len(got) != 4 {
		t.Errorf("a refused list changed the config: %q", got)
	}

	if err := o.SetSkipIfaces(nil); err != nil {
		t.Fatal(err)
	}
	if got := loadConfig(t, o).SkipIfaces; len(got) != 0 {
		t.Errorf("empty list loads as %q", got)
	}
}

func TestSetIPInfoURL(t *testing.T) {
	o := &Ops{P: paths.Under(t.TempDir())}
	if err := o.SetIPInfoURL("https://ip.example.org/json?token=x"); err != nil {
		t.Fatal(err)
	}
	if got := loadConfig(t, o).IPInfoURL; got != "https://ip.example.org/json?token=x" {
		t.Errorf("saved %q", got)
	}
	for _, bad := range []string{"http://ipinfo.io/json", "ipinfo.io/json", "https://", "https:///json", "ftp://x.example", "https://a b"} {
		rejects(t, o.SetIPInfoURL(bad), "https://")
	}
	if err := o.SetIPInfoURL(""); err != nil {
		t.Fatal(err)
	}
	if got := loadConfig(t, o).IPInfoURL; got != config.Default().IPInfoURL {
		t.Errorf("reset to %q", got)
	}
}

func TestSetQNum(t *testing.T) {
	o := &Ops{P: paths.Under(t.TempDir())}
	for _, n := range []int{0, 65535, 300} {
		if err := o.SetQNum(n); err != nil {
			t.Fatal(err)
		}
		if got := loadConfig(t, o).QNum; got != n {
			t.Errorf("saved %d, want %d", got, n)
		}
	}
	for _, bad := range []int{-1, 65536} {
		rejects(t, o.SetQNum(bad), "0", "65535")
	}
	if got := loadConfig(t, o).QNum; got != 300 {
		t.Errorf("a refused number changed the config: %d", got)
	}
}

func TestSetLangAndRunModeValidate(t *testing.T) {
	o := &Ops{P: paths.Under(t.TempDir())}
	rejects(t, o.SetLang("de"), `"de"`, "en, tr, ru")
	rejects(t, o.SetRunMode("later"), `"later"`, "window", "background", "boot")
	if err := o.SetLang("tr"); err != nil {
		t.Fatal(err)
	}
	if err := o.SetRunMode(config.RunBoot); err != nil {
		t.Fatal(err)
	}
	if c := loadConfig(t, o); c.Lang != "tr" || c.RunMode != config.RunBoot {
		t.Errorf("saved %q %q", c.Lang, c.RunMode)
	}
	if err := o.SetLang(""); err != nil {
		t.Fatal(err)
	}
	if c := loadConfig(t, o); c.Lang != "" {
		t.Errorf("lang %q after reset", c.Lang)
	}
}

// presetBundle is a preset tarball with one app.
func presetBundle(t *testing.T) []byte {
	t.Helper()
	files := map[string]string{
		"VERSION":         "2099-01-01",
		"apps/fresh.toml": "schema = 1\nid = \"fresh\"\nname = \"Fresh\"\nhosts = [\"fresh.example\"]\nprobe = [\"https://fresh.example/\"]\n",
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body))}); err != nil {
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

// localClient sends every request to srv, whatever its URL.
func localClient(srv *httptest.Server, asked *string) *http.Client {
	target, _ := url.Parse(srv.URL)
	tr := srv.Client().Transport
	return &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		*asked = r.URL.String()
		r = r.Clone(r.Context())
		r.URL.Scheme, r.URL.Host = target.Scheme, target.Host
		return tr.RoundTrip(r)
	})}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestPresetsUpdate(t *testing.T) {
	body := presetBundle(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Write(body) }))
	defer srv.Close()
	var asked string
	o := &Ops{P: paths.Under(t.TempDir()), HTTP: localClient(srv, &asked)}

	version, apps, err := o.PresetsUpdate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if version != "2099-01-01" || apps != 1 {
		t.Errorf("got %q %d", version, apps)
	}
	if asked != presets.UpdateURL {
		t.Errorf("asked %s", asked)
	}
	if s := o.PresetsList(); s.Origin != "cache" || s.Apps["fresh"].Name != "Fresh" {
		t.Errorf("cache not swapped: %s %v", s.Origin, s.Apps)
	}
}

func TestPresetsUpdateFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) }))
	defer srv.Close()
	var asked string
	o := &Ops{P: paths.Under(t.TempDir()), HTTP: localClient(srv, &asked)}
	if _, _, err := o.PresetsUpdate(context.Background()); err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("err %v", err)
	}
	if s := o.PresetsList(); s.Origin != "embedded" {
		t.Errorf("origin %s", s.Origin)
	}
}
