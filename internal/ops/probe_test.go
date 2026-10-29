package ops

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"testing"

	"golang.org/x/net/dns/dnsmessage"

	"github.com/KadirBerkpolat1/SplitDPI/internal/config"
	"github.com/KadirBerkpolat1/SplitDPI/internal/paths"
	"github.com/KadirBerkpolat1/SplitDPI/internal/probe"
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

// noAddressHTTP fails every probe in DNS and answers DoH: nothing.example
// does not exist, every other name has an A record.
type noAddressHTTP struct{}

func (noAddressHTTP) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Path != "/dns-query" {
		return nil, &net.DNSError{Err: "no such host", Name: r.URL.Hostname(), IsNotFound: true}
	}
	body, _ := io.ReadAll(r.Body)
	var q dnsmessage.Message
	if err := q.Unpack(body); err != nil {
		return nil, err
	}
	resp := dnsmessage.Message{Header: dnsmessage.Header{ID: q.ID, Response: true}, Questions: q.Questions}
	switch qq := q.Questions[0]; {
	case qq.Name.String() == "nothing.example.":
		resp.RCode = dnsmessage.RCodeNameError
	case qq.Type == dnsmessage.TypeA:
		resp.Answers = []dnsmessage.Resource{{
			Header: dnsmessage.ResourceHeader{Name: qq.Name, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET},
			Body:   &dnsmessage.AResource{A: [4]byte{104, 16, 0, 1}},
		}}
	}
	b, err := resp.Pack()
	if err != nil {
		return nil, err
	}
	return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(b)), Request: r}, nil
}

// A site the system and encrypted DNS both say has no address is not
// tested; one only encrypted DNS finds stays a DNS failure.
func TestProbeNoAddress(t *testing.T) {
	o := &Ops{P: paths.Under(t.TempDir()), HTTP: &http.Client{Transport: noAddressHTTP{}}}
	old := lookupSystemDNS
	lookupSystemDNS = func(_ context.Context, name string) ([]netip.Addr, error) {
		return nil, &net.DNSError{Err: "no such host", Name: name, IsNotFound: true}
	}
	t.Cleanup(func() { lookupSystemDNS = old })
	for _, d := range []string{"nothing.example", "hidden.example"} {
		if _, err := o.SitesAdd(d); err != nil {
			t.Fatal(err)
		}
	}
	res, err := o.Probe(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := probe.Overall(res["nothing.example"]); got != probe.NoAddress {
		t.Errorf("nothing.example: %q, %+v", got, res["nothing.example"])
	}
	if got := probe.Overall(res["hidden.example"]); got != probe.DNS {
		t.Errorf("hidden.example: %q, %+v", got, res["hidden.example"])
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
