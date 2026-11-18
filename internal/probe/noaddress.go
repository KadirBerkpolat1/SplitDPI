package probe

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"net/url"
)

// Lookup resolves a host. No addresses and no error means the resolver
// answered that the host has none; an error means it could not tell.
type Lookup func(context.Context, string) ([]netip.Addr, error)

// MarkNoAddress sets NoAddress on the failed results whose host has no
// usable address by both sys and doh: none at all, or only loopback and
// unspecified ones. Only results that failed in DNS or dialed such an
// address are looked up, each host once, up to maxParallel hosts at once;
// once ctx is done no more hosts are looked up. When doh finds an address
// the system does not, the failure stays: that is what tampering looks like.
func MarkNoAddress(ctx context.Context, rs []Result, sys, doh Lookup) {
	hostOf := make([]string, len(rs))
	index := map[string]int{}
	var hosts []string
	for i, r := range rs {
		if r.OK || (classifyErr(r.Cause) != DNS && !dialedNowhere(r.Cause)) {
			continue
		}
		u, err := url.Parse(r.URL)
		if err != nil || u.Hostname() == "" {
			continue
		}
		h := u.Hostname()
		hostOf[i] = h
		if _, ok := index[h]; !ok {
			index[h] = len(hosts)
			hosts = append(hosts, h)
		}
	}
	none := make([]bool, len(hosts))
	each(ctx, len(hosts), func(i int) {
		none[i] = noAddress(ctx, hosts[i], sys) && noAddress(ctx, hosts[i], doh)
	})
	for i, h := range hostOf {
		if h != "" {
			rs[i].NoAddress = none[index[h]]
		}
	}
}

func noAddress(ctx context.Context, host string, lookup Lookup) bool {
	addrs, err := lookup(ctx, host)
	if err != nil {
		return false
	}
	for _, a := range addrs {
		if !nowhere(a) {
			return false
		}
	}
	return true
}

// nowhere is an address that cannot be the site: loopback or unspecified.
func nowhere(a netip.Addr) bool {
	a = a.Unmap()
	return a.IsLoopback() || a.IsUnspecified()
}

// dialedNowhere reports whether err is a failed connection to a loopback or
// unspecified address.
func dialedNowhere(err error) bool {
	var op *net.OpError
	if !errors.As(err, &op) || op.Op != "dial" {
		return false
	}
	tcp, ok := op.Addr.(*net.TCPAddr)
	if !ok {
		return false
	}
	a, ok := netip.AddrFromSlice(tcp.IP)
	return ok && nowhere(a)
}
