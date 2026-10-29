package ops

import (
	"context"
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/KadirBerkpolat1/SplitDPI/internal/config"
	"github.com/KadirBerkpolat1/SplitDPI/internal/paths"
)

// fakeHTTP answers every request with 200 and records the URLs.
type fakeHTTP struct {
	mu   sync.Mutex
	urls []string
}

func (f *fakeHTTP) RoundTrip(r *http.Request) (*http.Response, error) {
	f.mu.Lock()
	f.urls = append(f.urls, r.URL.String())
	f.mu.Unlock()
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
}

func (f *fakeHTTP) seen() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.urls)
}

func TestProbeIncludesSites(t *testing.T) {
	h := &fakeHTTP{}
	o := &Ops{P: paths.Under(t.TempDir()), HTTP: &http.Client{Transport: h}}
	c := config.Default()
	c.Apps = []string{"discord"}
	if err := config.Save(o.P, c); err != nil {
		t.Fatal(err)
	}
	if _, err := o.SitesAdd("pastebin.com"); err != nil {
		t.Fatal(err)
	}
	res, err := o.Probe(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if rs := res["pastebin.com"]; len(rs) != 1 || rs[0].URL != "https://pastebin.com/" || !rs[0].OK {
		t.Errorf("site results %+v", rs)
	}
	if len(res["discord"]) == 0 {
		t.Errorf("app results missing: %+v", res)
	}
	if !slices.Contains(h.seen(), "https://pastebin.com/") {
		t.Errorf("requests %q", h.seen())
	}
}

func TestProbeSitesWithoutApps(t *testing.T) {
	o := &Ops{P: paths.Under(t.TempDir()), HTTP: &http.Client{Transport: &fakeHTTP{}}}
	if _, err := o.Probe(context.Background(), nil); !errors.As(err, &NoAppsError{}) {
		t.Fatalf("no apps, no sites: got %v, want NoAppsError", err)
	}
	if _, err := o.SitesAdd("pastebin.com"); err != nil {
		t.Fatal(err)
	}
	res, err := o.Probe(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || len(res["pastebin.com"]) != 1 {
		t.Errorf("results %+v", res)
	}
}
