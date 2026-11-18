package probe

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"slices"

	"github.com/KadirBerkpolat1/SplitDPI/internal/dns"
)

// DNSResult compares system resolver answers with DoH. Err is the lookup
// that failed, with nothing compared.
type DNSResult struct {
	Domain   string
	System   []netip.Addr
	DoH      []netip.Addr
	Poisoned bool
	Mismatch bool
	Reason   string
	Err      error
}

// CheckDNS resolves each domain through sys and doh, up to maxParallel
// domains at once, and classifies poisoning. An answer that the name has no
// address is an answer, not a failure: an ISP may block a name with NXDOMAIN
// or an empty answer. Once ctx is done the domains not yet resolved fail
// with its error.
func CheckDNS(ctx context.Context, domains []string, sys func(context.Context, string) ([]netip.Addr, error), doh func(context.Context, string) ([]netip.Addr, error)) []DNSResult {
	out := make([]DNSResult, len(domains))
	each(ctx, len(domains), func(i int) {
		out[i] = checkDomain(ctx, domains[i], sys, doh)
	})
	for i, d := range domains {
		if out[i].Domain == "" {
			err := ctx.Err()
			out[i] = DNSResult{Domain: d, Reason: err.Error(), Err: err}
		}
	}
	return out
}

func checkDomain(ctx context.Context, d string, sys, doh func(context.Context, string) ([]netip.Addr, error)) DNSResult {
	r := DNSResult{Domain: d}
	var err error
	r.System, err = sys(ctx, d)
	if err != nil && !noAddressAnswer(err) {
		r.Reason, r.Err = err.Error(), err
		return r
	}
	r.DoH, err = doh(ctx, d)
	if err != nil && !noAddressAnswer(err) {
		r.Reason, r.Err = err.Error(), err
		return r
	}
	classifyDNS(&r)
	return r
}

// noAddressAnswer reports whether err is a resolver's answer that the name
// has no address: the system resolver's not-found, or dns.ErrNoAddress.
func noAddressAnswer(err error) bool {
	var dnsErr *net.DNSError
	return errors.As(err, &dnsErr) && dnsErr.IsNotFound || errors.Is(err, dns.ErrNoAddress)
}

func classifyDNS(r *DNSResult) {
	if containsKnownBlock(r.System) {
		r.Poisoned = true
		r.Reason = "known block page"
		return
	}
	if len(r.System) == 0 && anyPublic(r.DoH) {
		r.Poisoned = true
		r.Reason = "system resolver returned no address"
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
