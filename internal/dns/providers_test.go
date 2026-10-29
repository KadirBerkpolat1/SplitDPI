package dns_test

import (
	"slices"
	"testing"

	"github.com/KadirBerkpolat1/SplitDPI/internal/dns"
)

func TestValid(t *testing.T) {
	for _, id := range []string{"off", "auto", "cloudflare", "google", "quad9", "adguard"} {
		if !dns.Valid(id) {
			t.Errorf("%s not valid", id)
		}
	}
	for _, id := range []string{"", "on", "Cloudflare", "doq"} {
		if dns.Valid(id) {
			t.Errorf("%q valid", id)
		}
	}
}

func addrs(us []dns.Upstream) []string {
	var out []string
	for _, u := range us {
		out = append(out, string(u.Protocol)+" "+u.Addr.String())
	}
	return out
}

func TestUpstreams(t *testing.T) {
	for _, tc := range []struct {
		provider string
		proto    dns.Protocol
		want     []string
	}{
		// auto is v0.1's fixed order.
		{"auto", dns.DoH, []string{"doh 1.1.1.1:443", "doh 8.8.8.8:443", "doh 9.9.9.9:443"}},
		{"auto", dns.DoT, []string{"dot 1.1.1.1:853", "dot 8.8.8.8:853", "dot 9.9.9.9:853"}},
		{"cloudflare", dns.DoH, []string{"doh 1.1.1.1:443", "doh 1.0.0.1:443"}},
		{"google", dns.DoT, []string{"dot 8.8.8.8:853", "dot 8.8.4.4:853"}},
		{"quad9", dns.DoH, []string{"doh 9.9.9.9:443", "doh 149.112.112.112:443"}},
		{"adguard", dns.DoT, []string{"dot 94.140.14.14:853", "dot 94.140.15.15:853"}},
		{"off", dns.DoH, nil},
		{"nope", dns.DoH, nil},
	} {
		if got := addrs(dns.Upstreams(tc.provider, tc.proto)); !slices.Equal(got, tc.want) {
			t.Errorf("%s/%s: %q, want %q", tc.provider, tc.proto, got, tc.want)
		}
	}
	if got := dns.Upstreams("auto", dns.DoH)[0].URL(); got != "https://1.1.1.1/dns-query" {
		t.Errorf("first auto URL %s", got)
	}
}

func TestProtocolName(t *testing.T) {
	if dns.DoH.Name() != "DNS-over-HTTPS" || dns.DoT.Name() != "DNS-over-TLS" {
		t.Errorf("%s, %s", dns.DoH.Name(), dns.DoT.Name())
	}
}
