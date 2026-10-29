package ops

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/fstest"

	"golang.org/x/net/dns/dnsmessage"

	"github.com/KadirBerkpolat1/SplitDPI/internal/config"
	"github.com/KadirBerkpolat1/SplitDPI/internal/dns"
	"github.com/KadirBerkpolat1/SplitDPI/internal/paths"
	"github.com/KadirBerkpolat1/SplitDPI/internal/probe"
	"github.com/KadirBerkpolat1/SplitDPI/internal/scan"
)

func TestDNSProviders(t *testing.T) {
	o := &Ops{}
	var ids []string
	for _, p := range o.DNSProviders() {
		ids = append(ids, p.ID)
		if p.Name == "" || p.Note == "" || strings.HasPrefix(p.Name, "!") || strings.HasPrefix(p.Note, "!") {
			t.Errorf("%+v", p)
		}
		if p.ID == "adguard" && !strings.Contains(p.Note, "ads") {
			t.Errorf("adguard note %q", p.Note)
		}
	}
	if want := []string{"off", "auto", "cloudflare", "google", "quad9", "adguard"}; !slices.Equal(ids, want) {
		t.Errorf("ids %q, want %q", ids, want)
	}
}

func TestSetDNS(t *testing.T) {
	o := &Ops{P: paths.Under(t.TempDir())}
	if s, err := o.DNSSetting(); err != nil || s != (DNSSetting{Provider: "off", Protocol: dns.DoH}) {
		t.Fatalf("default %+v %v", s, err)
	}
	running, err := o.SetDNS("quad9", dns.DoT)
	if err != nil || running {
		t.Fatalf("running %v, err %v", running, err)
	}
	if s, _ := o.DNSSetting(); s != (DNSSetting{Provider: "quad9", Protocol: dns.DoT}) {
		t.Errorf("after set %+v", s)
	}
	for _, bad := range [][2]string{{"on", "doh"}, {"quad9", "doq"}} {
		if _, err := o.SetDNS(bad[0], dns.Protocol(bad[1])); err == nil {
			t.Errorf("%v accepted", bad)
		}
	}
	if s, _ := o.DNSSetting(); s.Provider != "quad9" {
		t.Errorf("bad set changed config: %+v", s)
	}
}

func TestDNSClientFollowsConfig(t *testing.T) {
	o := &Ops{}
	c := config.Default()
	if got := o.dnsClient(c).Upstreams; !slices.Equal(got, dns.Upstreams("auto", dns.DoH)) {
		t.Errorf("off: %v", got)
	}
	c.DNSProvider, c.DNSProtocol = "adguard", dns.DoT
	if got := o.dnsClient(c).Upstreams; !slices.Equal(got, dns.Upstreams("adguard", dns.DoT)) {
		t.Errorf("adguard/dot: %v", got)
	}
}

// dohHTTP answers DoH A queries with 104.16.0.1 and records the hosts asked.
type dohHTTP struct {
	mu    sync.Mutex
	hosts []string
}

func (d *dohHTTP) RoundTrip(r *http.Request) (*http.Response, error) {
	d.mu.Lock()
	d.hosts = append(d.hosts, r.URL.Host)
	d.mu.Unlock()
	body, _ := io.ReadAll(r.Body)
	var q dnsmessage.Message
	if err := q.Unpack(body); err != nil {
		return &http.Response{StatusCode: 400, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
	}
	resp := dnsmessage.Message{Header: dnsmessage.Header{ID: q.ID, Response: true}, Questions: q.Questions}
	if q.Questions[0].Type == dnsmessage.TypeA {
		resp.Answers = []dnsmessage.Resource{{
			Header: dnsmessage.ResourceHeader{Name: q.Questions[0].Name, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET},
			Body:   &dnsmessage.AResource{A: [4]byte{104, 16, 0, 1}},
		}}
	}
	b, _ := resp.Pack()
	return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(b)), Request: r}, nil
}

func fakeSystemDNS(t *testing.T, answer string) {
	t.Helper()
	old := lookupSystemDNS
	lookupSystemDNS = func(context.Context, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr(answer)}, nil
	}
	t.Cleanup(func() { lookupSystemDNS = old })
}

// dnsScanOps runs site scans against a fake blockcheck2 that reports what DNS
// it was told to use and finds nothing, so no trial (and no forwarder) starts.
func dnsScanOps(t *testing.T) (*Ops, *dohHTTP) {
	t.Helper()
	o, _, _ := trialOps(t, fstest.MapFS{})
	script := "#!/bin/sh\necho \"dns SECURE_DNS=$SECURE_DNS DOH_SERVERS=$DOH_SERVERS\"\necho\necho '* SUMMARY'\n"
	if err := os.WriteFile(filepath.Join(o.P.EngineDir, "zapret2-v1.0.6", "blockcheck2.sh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	h := &dohHTTP{}
	o.HTTP = &http.Client{Transport: h}
	return o, h
}

func TestScanDNSCheck(t *testing.T) {
	const autoDoH = "dns SECURE_DNS=1 DOH_SERVERS=https://1.1.1.1/dns-query https://8.8.8.8/dns-query https://9.9.9.9/dns-query"
	for _, tc := range []struct {
		name      string
		provider  string // configured before the scan
		system    string // system resolver answer
		answer    string // what DNSCheck returns
		asked     bool
		env       string // blockcheck2's DNS line
		saved     string
		encrypted bool // dnscrypt-proxy runs
		force     bool // --dns given
	}{
		{name: "clean", provider: "off", system: "104.16.0.1", env: "dns SECURE_DNS= DOH_SERVERS=", saved: "off"},
		{name: "poisoned yes", provider: "off", system: "195.175.254.2", answer: "auto", asked: true, env: autoDoH, saved: "auto"},
		{name: "poisoned no", provider: "off", system: "195.175.254.2", asked: true, env: "dns SECURE_DNS= DOH_SERVERS=", saved: "off"},
		{name: "system encrypted", provider: "off", system: "195.175.254.2", answer: "auto", encrypted: true, env: "dns SECURE_DNS= DOH_SERVERS=", saved: "off"},
		{name: "system encrypted, --dns", provider: "off", system: "195.175.254.2", answer: "auto", encrypted: true, force: true, asked: true, env: autoDoH, saved: "auto"},
		{name: "already on", provider: "adguard", system: "195.175.254.2", env: "dns SECURE_DNS=1 DOH_SERVERS=https://94.140.14.14/dns-query https://94.140.15.15/dns-query", saved: "adguard"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o, h := dnsScanOps(t)
			fakeSystemDNS(t, tc.system)
			if tc.encrypted {
				o.Root = fstest.MapFS{"proc/321/stat": {Data: []byte("321 (dnscrypt-proxy) S 1 321 321 0 -1 0\n")}}
			}
			if _, err := o.SetDNS(tc.provider, dns.DoT); err != nil {
				t.Fatal(err)
			}
			var asked []probe.DNSResult
			var log []string
			opt := scan.Options{ASN: 9121, Site: "pastebin.com", DNSForce: tc.force, DNSCheck: func(_ context.Context, p []probe.DNSResult) string {
				asked = p
				return tc.answer
			}}
			o.Scan(context.Background(), opt, func(l string) { log = append(log, l) })
			if (asked != nil) != tc.asked {
				t.Errorf("asked %v, want %v", asked, tc.asked)
			}
			if tc.asked && (len(asked) != 1 || asked[0].Domain != "pastebin.com" || !asked[0].Poisoned) {
				t.Errorf("asked with %+v", asked)
			}
			if !slices.Contains(log, tc.env) {
				t.Errorf("blockcheck2 log %q, want %q", log, tc.env)
			}
			s, _ := o.DNSSetting()
			if s.Provider != tc.saved {
				t.Errorf("saved %+v, want %s", s, tc.saved)
			}
			if tc.asked && tc.answer != "" && s.Protocol != dns.DoH {
				t.Errorf("scan turned DNS on over %s", s.Protocol)
			}
			skipped := tc.encrypted && !tc.force
			if tc.provider == "off" && slices.Contains(h.hosts, "1.1.1.1") == skipped {
				t.Errorf("reference DoH asked %q, skipped %v", h.hosts, skipped)
			}
			skipLine := "System DNS is already encrypted (dnscrypt-proxy), so the DNS poisoning check is skipped."
			if slices.Contains(log, skipLine) != skipped {
				t.Errorf("log %q, skipped %v", log, skipped)
			}
		})
	}
}
