package sysinfo

import (
	"encoding/binary"
	"encoding/hex"
	"io/fs"
	"net/netip"
	"slices"
	"strconv"
	"strings"
)

// LocalDNS is the machine's normal DNS setup: the servers behind any local
// stub resolver, and the search domains.
type LocalDNS struct {
	Servers []netip.Addr
	Search  []string // lower case, no trailing dot
}

// resolvFiles are read in order; the first with a non-loopback nameserver
// wins. The stub resolvers' own files come first, as /etc/resolv.conf usually
// names only the stub at 127.0.0.53.
var resolvFiles = []string{
	"run/systemd/resolve/resolv.conf",
	"run/NetworkManager/no-stub-resolv.conf",
	"run/NetworkManager/resolv.conf",
	"etc/resolv.conf",
}

// ReadLocalDNS finds the servers in the resolver files, else the IPv4 default
// gateway, which is usually the router's DNS. Search domains come from all files.
func ReadLocalDNS(root fs.FS) LocalDNS {
	var d LocalDNS
	for _, name := range resolvFiles {
		b, err := fs.ReadFile(root, name)
		if err != nil {
			continue
		}
		servers, search := parseResolvConf(string(b))
		if d.Servers == nil {
			d.Servers = servers
		}
		for _, s := range search {
			if !slices.Contains(d.Search, s) {
				d.Search = append(d.Search, s)
			}
		}
	}
	if d.Servers == nil {
		if gw, ok := defaultGateway(root); ok {
			d.Servers = []netip.Addr{gw}
		}
	}
	return d
}

// parseResolvConf returns the non-loopback nameservers (nil when none) and the
// search domains of a resolv.conf.
func parseResolvConf(s string) (servers []netip.Addr, search []string) {
	for line := range strings.Lines(s) {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		switch f[0] {
		case "nameserver":
			if a, err := netip.ParseAddr(f[1]); err == nil && !a.Unmap().IsLoopback() && !a.IsUnspecified() {
				servers = append(servers, a)
			}
		case "search", "domain":
			for _, dom := range f[1:] {
				// systemd-resolved marks routing-only domains with "~".
				dom = strings.ToLower(strings.Trim(dom, "."))
				if dom != "" && !strings.HasPrefix(dom, "~") {
					search = append(search, dom)
				}
			}
		}
	}
	return servers, search
}

// defaultGateway is the IPv4 default route's gateway with the lowest metric,
// from /proc/net/route (little-endian hex fields).
func defaultGateway(root fs.FS) (netip.Addr, bool) {
	b, err := fs.ReadFile(root, "proc/net/route")
	if err != nil {
		return netip.Addr{}, false
	}
	const rtfGateway = 0x2
	var best netip.Addr
	bestMetric := -1
	for line := range strings.Lines(string(b)) {
		// Iface Destination Gateway Flags RefCnt Use Metric Mask ...
		f := strings.Fields(line)
		if len(f) < 8 || f[1] != "00000000" || f[7] != "00000000" {
			continue
		}
		flags, err := strconv.ParseUint(f[3], 16, 32)
		if err != nil || flags&rtfGateway == 0 {
			continue
		}
		raw, err := hex.DecodeString(f[2])
		metric, merr := strconv.Atoi(f[6])
		if err != nil || len(raw) != 4 || merr != nil {
			continue
		}
		var ip [4]byte
		binary.BigEndian.PutUint32(ip[:], binary.LittleEndian.Uint32(raw))
		if gw := netip.AddrFrom4(ip); !gw.IsUnspecified() && (bestMetric < 0 || metric < bestMetric) {
			best, bestMetric = gw, metric
		}
	}
	return best, bestMetric >= 0
}
