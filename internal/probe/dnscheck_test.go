package probe_test

import (
	"context"
	"net/netip"
	"testing"

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
