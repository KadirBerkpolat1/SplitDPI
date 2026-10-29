package ops

import (
	"slices"
	"testing"

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
