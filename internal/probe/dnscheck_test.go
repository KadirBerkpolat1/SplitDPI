package probe_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"testing"

	"github.com/KadirBerkpolat1/SplitDPI/internal/dns"
	"github.com/KadirBerkpolat1/SplitDPI/internal/probe"
)

func addr(s string) netip.Addr { return netip.MustParseAddr(s) }

func checkOne(t *testing.T, domain string, sys, doh []netip.Addr) probe.DNSResult {
	t.Helper()
	rs := probe.CheckDNS(context.Background(), []string{domain},
		func(context.Context, string) ([]netip.Addr, error) { return sys, nil },
		func(context.Context, string) ([]netip.Addr, error) { return doh, nil },
	)
	if len(rs) != 1 {
		t.Fatalf("len %d", len(rs))
	}
	return rs[0]
}

func TestCheckDNSKnownBlock(t *testing.T) {
	r := checkOne(t, "discord.com", []netip.Addr{addr("195.175.254.2")}, []netip.Addr{addr("162.159.128.233")})
	if !r.Poisoned || r.Reason != "known block page" {
		t.Fatalf("got %+v", r)
	}
}

func TestCheckDNSUnspecifiedSystem(t *testing.T) {
	r := checkOne(t, "discord.com", []netip.Addr{addr("0.0.0.0")}, []netip.Addr{addr("162.159.128.233")})
	if !r.Poisoned {
		t.Fatalf("got %+v", r)
	}
}

func TestCheckDNSMismatchOnly(t *testing.T) {
	r := checkOne(t, "discord.com",
		[]netip.Addr{addr("104.16.132.229")},
		[]netip.Addr{addr("162.159.128.233")})
	if r.Poisoned || !r.Mismatch {
		t.Fatalf("got %+v", r)
	}
}

// lookup answers addrs, or fails with err.
func lookup(addrs []netip.Addr, err error) func(context.Context, string) ([]netip.Addr, error) {
	return func(context.Context, string) ([]netip.Addr, error) { return addrs, err }
}

// An ISP may block a name with NXDOMAIN or an empty answer: with a public
// address over DoH, that is tampering. A name neither has is just missing,
// and a lookup that failed compares nothing.
func TestDNSCheckEmptyAnswerIsTampering(t *testing.T) {
	public := []netip.Addr{addr("162.159.128.233")}
	nxdomain := &net.DNSError{Err: "no such host", Name: "discord.com", IsNotFound: true}
	dohNone := fmt.Errorf("discord.com: %w", dns.ErrNoAddress)
	timeout := &net.DNSError{Err: "i/o timeout", Name: "discord.com", IsTimeout: true}
	for _, tc := range []struct {
		name           string
		sys, doh       func(context.Context, string) ([]netip.Addr, error)
		poisoned, fail bool
	}{
		{"empty answer", lookup(nil, nil), lookup(public, nil), true, false},
		{"nxdomain", lookup(nil, nxdomain), lookup(public, nil), true, false},
		{"missing everywhere", lookup(nil, nxdomain), lookup(nil, dohNone), false, false},
		{"system timeout", lookup(nil, timeout), lookup(public, nil), false, true},
		{"doh down", lookup(public, nil), lookup(nil, errors.New("9.9.9.9:443: connection refused")), false, true},
	} {
		r := probe.CheckDNS(context.Background(), []string{"discord.com"}, tc.sys, tc.doh)[0]
		if r.Poisoned != tc.poisoned || (r.Err != nil) != tc.fail || r.Mismatch {
			t.Errorf("%s: got %+v, want poisoned %v, failed %v", tc.name, r, tc.poisoned, tc.fail)
		}
	}
}

func TestDNSCheckTTIPv6BlockAddress(t *testing.T) {
	r := checkOne(t, "discord.com", []netip.Addr{addr("2a01:358:4014:a00::3")}, []netip.Addr{addr("2606:4700::6810:84e5")})
	if !r.Poisoned || r.Reason != "known block page" {
		t.Fatalf("got %+v", r)
	}
}
