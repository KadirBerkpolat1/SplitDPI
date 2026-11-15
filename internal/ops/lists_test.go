package ops

import (
	"context"
	"os"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/KadirBerkpolat1/SplitDPI/internal/config"
	"github.com/KadirBerkpolat1/SplitDPI/internal/paths"
)

func TestAcceptDisclaimer(t *testing.T) {
	o := &Ops{P: paths.Under(t.TempDir())}
	if err := o.AcceptDisclaimer(); err != nil {
		t.Fatal(err)
	}
	c, err := config.Load(o.P)
	if err != nil || !c.DisclaimerAccepted {
		t.Errorf("got %+v, %v", c, err)
	}
}

func TestSetLang(t *testing.T) {
	o := &Ops{P: paths.Under(t.TempDir())}
	if err := o.AcceptDisclaimer(); err != nil {
		t.Fatal(err)
	}
	if err := o.SetLang("tr"); err != nil {
		t.Fatal(err)
	}
	c, err := config.Load(o.P)
	if err != nil || c.Lang != "tr" || !c.DisclaimerAccepted {
		t.Errorf("got %+v, %v", c, err)
	}
}

func TestSetRunMode(t *testing.T) {
	o := &Ops{P: paths.Under(t.TempDir())}
	if err := o.SetLang("tr"); err != nil {
		t.Fatal(err)
	}
	if err := o.SetRunMode(config.RunBoot); err != nil {
		t.Fatal(err)
	}
	c, err := config.Load(o.P)
	if err != nil || c.RunMode != config.RunBoot || c.Lang != "tr" {
		t.Errorf("got %+v, %v", c, err)
	}
}

func TestSites(t *testing.T) {
	o := &Ops{P: paths.Under(t.TempDir())}
	if list, err := o.SitesList(); err != nil || len(list) != 0 {
		t.Fatalf("empty list: %q, %v", list, err)
	}
	for _, in := range []string{"https://PasteBin.com/raw/x", "pastebin.com", "*.pastebin.com.", "Example.org"} {
		if _, err := o.SitesAdd(in); err != nil {
			t.Fatalf("add %q: %v", in, err)
		}
	}
	if d, _ := o.SitesAdd("HTTPS://Pastebin.COM"); d != "pastebin.com" {
		t.Errorf("normalized %q", d)
	}
	if list, _ := o.SitesList(); !slices.Equal(list, []string{"pastebin.com", "example.org"}) {
		t.Errorf("after add: %q", list)
	}
	if _, err := o.SitesAdd("1.2.3.4"); err == nil {
		t.Error("an IP address was accepted as a site")
	}
	if err := o.SitesRemove("https://PASTEBIN.com/"); err != nil {
		t.Fatal(err)
	}
	if list, _ := o.SitesList(); !slices.Equal(list, []string{"example.org"}) {
		t.Errorf("after remove: %q", list)
	}
}

// A site added while SplitDPI runs with encrypted DNS must not wait for an
// answer cached before; without the redirect, the ISP would answer again.
func TestSitesAddFlushesDNSWhileRunning(t *testing.T) {
	var ran []string
	o := &Ops{
		P:    paths.Under(t.TempDir()),
		Root: fstest.MapFS{"proc/42/stat": {Data: []byte("42 (systemd-resolve) S 1 42 42 0 -1")}},
		Exec: func(_ context.Context, _, name string, args ...string) ([]byte, error) {
			ran = append(ran, strings.Join(append([]string{name}, args...), " "))
			return nil, nil
		},
	}
	add := func(d string) {
		t.Helper()
		if _, err := o.SitesAdd(d); err != nil {
			t.Fatal(err)
		}
	}
	add("a.example.com")
	if len(ran) != 0 {
		t.Errorf("not running: ran %q", ran)
	}
	holdRunLock(t, o.P, "service", os.Getpid())
	add("b.example.com")
	if len(ran) != 0 {
		t.Errorf("running with dns off: ran %q", ran)
	}
	c, err := config.Load(o.P)
	if err != nil {
		t.Fatal(err)
	}
	c.DNSProvider = "auto"
	if err := config.Save(o.P, c); err != nil {
		t.Fatal(err)
	}
	add("c.example.com")
	if !slices.Equal(ran, []string{"resolvectl flush-caches"}) {
		t.Errorf("running with dns on: ran %q", ran)
	}
}
