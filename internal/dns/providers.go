package dns

import (
	"net/netip"
	"slices"
)

// Protocol is how splitdpi talks to a provider.
type Protocol string

const (
	DoH Protocol = "doh"
	DoT Protocol = "dot"
)

// Name is the protocol's full name, the same in every language.
func (p Protocol) Name() string {
	if p == DoT {
		return "DNS-over-TLS"
	}
	return "DNS-over-HTTPS"
}

// Off and Auto are the dns_provider values that are not a single provider.
const (
	Off  = "off"
	Auto = "auto"
)

// Provider is a public resolver reached by IP literal. Every IP here was
// checked to present a certificate valid for that IP on 443 and 853 without
// SNI, so no resolver is needed to find it and its hostname never leaves us.
type Provider struct {
	ID   string
	Name string
	IPs  []netip.Addr // primary first
}

var Providers = []Provider{
	{ID: "cloudflare", Name: "Cloudflare", IPs: addrs("1.1.1.1", "1.0.0.1")},
	{ID: "google", Name: "Google", IPs: addrs("8.8.8.8", "8.8.4.4")},
	{ID: "quad9", Name: "Quad9", IPs: addrs("9.9.9.9", "149.112.112.112")},
	{ID: "adguard", Name: "AdGuard", IPs: addrs("94.140.14.14", "94.140.15.15")},
}

// autoOrder is tried by Auto: each provider's primary IP in turn.
var autoOrder = []string{"cloudflare", "google", "quad9"}

func addrs(ss ...string) []netip.Addr {
	out := make([]netip.Addr, len(ss))
	for i, s := range ss {
		out[i] = netip.MustParseAddr(s)
	}
	return out
}

// Lookup finds a provider by ID.
func Lookup(id string) (Provider, bool) {
	i := slices.IndexFunc(Providers, func(p Provider) bool { return p.ID == id })
	if i < 0 {
		return Provider{}, false
	}
	return Providers[i], true
}

// Valid reports whether id is a dns_provider value: Off, Auto or a provider.
func Valid(id string) bool {
	_, ok := Lookup(id)
	return ok || id == Off || id == Auto
}

// Upstreams lists the servers for provider over proto, in the order tried.
// It returns nil for Off and unknown IDs.
func Upstreams(provider string, proto Protocol) []Upstream {
	var ips []netip.Addr
	if provider == Auto {
		for _, id := range autoOrder {
			p, _ := Lookup(id)
			ips = append(ips, p.IPs[0])
		}
	} else if p, ok := Lookup(provider); ok {
		ips = p.IPs
	}
	port := uint16(443)
	if proto == DoT {
		port = 853
	}
	var out []Upstream
	for _, ip := range ips {
		out = append(out, Upstream{Protocol: proto, Addr: netip.AddrPortFrom(ip, port)})
	}
	return out
}
