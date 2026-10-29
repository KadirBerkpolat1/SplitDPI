package probe

import (
	"context"
	"net/netip"
	"slices"
)

// DNSResult compares system resolver answers with DoH.
type DNSResult struct {
	Domain    string
	System    []netip.Addr
	DoH       []netip.Addr
	Poisoned  bool
	Mismatch  bool
	Reason    string
}

// CheckDNS resolves each domain through sys and doh and classifies poisoning.
func CheckDNS(ctx context.Context, domains []string, sys func(context.Context, string) ([]netip.Addr, error), doh func(context.Context, string) ([]netip.Addr, error)) []DNSResult {
	out := make([]DNSResult, len(domains))
	for i, d := range domains {
		r := DNSResult{Domain: d}
		var err error
		r.System, err = sys(ctx, d)
		if err != nil {
			r.Reason = err.Error()
			out[i] = r
			continue
		}
		r.DoH, err = doh(ctx, d)
		if err != nil {
			r.Reason = err.Error()
			out[i] = r
			continue
		}
		classifyDNS(&r)
		out[i] = r
	}
	return out
}

func classifyDNS(r *DNSResult) {
	if containsKnownBlock(r.System) {
		r.Poisoned = true
		r.Reason = "known block page"
		return
	}
	if systemNonPublicDoHPublic(r.System, r.DoH) {
		r.Poisoned = true
		if r.Reason == "" {
			r.Reason = "system resolver returned non-public address"
		}
		return
	}
	if publicMismatch(r.System, r.DoH) {
		r.Mismatch = true
	}
}

func containsKnownBlock(addrs []netip.Addr) bool {
	for _, a := range addrs {
		if slices.Contains(KnownBlockIPs, a) {
			return true
		}
	}
	return false
}

func systemNonPublicDoHPublic(sys, doh []netip.Addr) bool {
	if !anyPublic(doh) {
		return false
	}
	for _, a := range sys {
		if !isPublic(a) {
			return true
		}
	}
	return false
}

func publicMismatch(sys, doh []netip.Addr) bool {
	if !allPublic(sys) || !allPublic(doh) {
		return false
	}
	return !addrSetEqual(sys, doh)
}

func isPublic(a netip.Addr) bool {
	return a.IsValid() && !a.IsLoopback() && !a.IsPrivate() && !a.IsUnspecified()
}

func anyPublic(addrs []netip.Addr) bool {
	for _, a := range addrs {
		if isPublic(a) {
			return true
		}
	}
	return false
}

func allPublic(addrs []netip.Addr) bool {
	if len(addrs) == 0 {
		return false
	}
	for _, a := range addrs {
		if !isPublic(a) {
			return false
		}
	}
	return true
}

func addrSetEqual(a, b []netip.Addr) bool {
	if len(a) != len(b) {
		return false
	}
	aa := append([]netip.Addr(nil), a...)
	bb := append([]netip.Addr(nil), b...)
	slices.SortFunc(aa, func(x, y netip.Addr) int { return x.Compare(y) })
	slices.SortFunc(bb, func(x, y netip.Addr) int { return x.Compare(y) })
	return slices.Equal(aa, bb)
}
