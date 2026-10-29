package dns

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/net/dns/dnsmessage"

	"github.com/KadirBerkpolat1/SplitDPI/internal/nft"
	"github.com/KadirBerkpolat1/SplitDPI/internal/sysinfo"
)

// localRefresh bounds how stale the local servers may be: laptops change networks.
const localRefresh = 30 * time.Second

// Local resolves the names only the local network knows (printer.lan, the
// search domains, reverse lookups of private addresses) with the machine's
// normal DNS servers over plain DNS. The encrypted provider cannot answer
// them, and they must not leak to it.
type Local struct {
	Root fs.FS // the resolver files and proc/net/route are read from here
	// Dial opens the plain DNS sockets. nil marks them with nft.LocalDNSMark,
	// which the redirect lets through; an unmarked socket would loop back to us.
	Dial func(ctx context.Context, network, addr string) (net.Conn, error)
	Now  func() time.Time // nil is time.Now

	mu   sync.Mutex
	read time.Time
	dns  sysinfo.LocalDNS
}

// Has reports whether name (with or without the trailing dot) is local.
func (l *Local) Has(name string) bool {
	return isLocalName(name, l.current().Search)
}

// Exchange relays q to the local servers in order, UDP first and TCP when the
// answer is truncated, until one answers within 2 s.
func (l *Local) Exchange(ctx context.Context, q []byte) ([]byte, error) {
	last := errors.New("no local DNS servers")
	for _, s := range l.current().Servers {
		uctx, cancel := context.WithTimeout(ctx, upstreamTimeout)
		resp, err := l.exchange(uctx, netip.AddrPortFrom(s, 53).String(), q)
		cancel()
		if err == nil {
			return resp, nil
		}
		last = fmt.Errorf("%s: %w", s, err)
	}
	return nil, last
}

func (l *Local) current() sysinfo.LocalDNS {
	now := time.Now
	if l.Now != nil {
		now = l.Now
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if t := now(); l.read.IsZero() || t.Sub(l.read) >= localRefresh {
		l.dns, l.read = sysinfo.ReadLocalDNS(l.Root), t
	}
	return l.dns
}

func (l *Local) exchange(ctx context.Context, addr string, q []byte) ([]byte, error) {
	resp, err := l.udp(ctx, addr, q)
	if err != nil {
		return nil, err
	}
	var p dnsmessage.Parser
	if h, err := p.Start(resp); err != nil || !h.Truncated {
		return resp, err
	}
	conn, err := l.dial(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	return tcpRoundTrip(ctx, conn, q)
}

// udp sends q once and waits for the answer with its ID, skipping strays.
func (l *Local) udp(ctx context.Context, addr string, q []byte) ([]byte, error) {
	if len(q) < 2 {
		return nil, errors.New("short DNS query")
	}
	conn, err := l.dial(ctx, "udp", addr)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	if dl, ok := ctx.Deadline(); ok {
		conn.SetDeadline(dl)
	}
	stop := context.AfterFunc(ctx, func() { conn.SetDeadline(time.Unix(1, 0)) })
	defer stop()
	if _, err := conn.Write(q); err != nil {
		return nil, err
	}
	buf := make([]byte, 65535)
	for {
		n, err := conn.Read(buf)
		if err != nil {
			return nil, err
		}
		var p dnsmessage.Parser
		if h, err := p.Start(buf[:n]); err == nil && h.Response && h.ID == uint16(q[0])<<8|uint16(q[1]) {
			return append([]byte(nil), buf[:n]...), nil
		}
	}
}

func (l *Local) dial(ctx context.Context, network, addr string) (net.Conn, error) {
	if l.Dial != nil {
		return l.Dial(ctx, network, addr)
	}
	d := net.Dialer{Control: func(_, _ string, c syscall.RawConn) error {
		var serr error
		if err := c.Control(func(fd uintptr) {
			serr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_MARK, nft.LocalDNSMark)
		}); err != nil {
			return err
		}
		return serr
	}}
	return d.DialContext(ctx, network, addr)
}

// localSuffixes never resolve on the public internet (RFC 6762, RFC 8375,
// ICANN's .internal) or are common router defaults.
var localSuffixes = []string{"lan", "local", "home.arpa", "internal", "localdomain"}

// localNets are the ranges whose reverse lookups only the local network can answer.
var localNets = []netip.Prefix{
	netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("169.254.0.0/16"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("fc00::/7"),
	netip.MustParsePrefix("fe80::/10"),
	netip.MustParsePrefix("::1/128"),
}

// isLocalName reports whether name belongs to the local network: a single
// label, a local suffix or search domain, or a reverse lookup inside localNets.
func isLocalName(name string, search []string) bool {
	name = strings.ToLower(strings.TrimSuffix(name, "."))
	if name == "" {
		return false
	}
	if !strings.Contains(name, ".") || under(name, localSuffixes) || under(name, search) {
		return true
	}
	p, ok := reversePrefix(name)
	if !ok {
		return false
	}
	for _, n := range localNets {
		if p.Bits() >= n.Bits() && n.Contains(p.Addr()) {
			return true
		}
	}
	return false
}

// under reports whether name is one of domains or a subdomain of one.
func under(name string, domains []string) bool {
	for _, d := range domains {
		if rest, ok := strings.CutSuffix(name, d); ok && (rest == "" || strings.HasSuffix(rest, ".")) {
			return true
		}
	}
	return false
}

// reversePrefix turns an in-addr.arpa or ip6.arpa name into the network it
// names: "168.192.in-addr.arpa" is 192.168.0.0/16.
func reversePrefix(name string) (netip.Prefix, bool) {
	if rest, ok := strings.CutSuffix(name, ".in-addr.arpa"); ok {
		labels := strings.Split(rest, ".")
		if len(labels) > 4 {
			return netip.Prefix{}, false
		}
		var ip [4]byte
		for i, l := range labels {
			v, err := strconv.ParseUint(l, 10, 8)
			if err != nil {
				return netip.Prefix{}, false
			}
			ip[len(labels)-1-i] = byte(v)
		}
		return netip.PrefixFrom(netip.AddrFrom4(ip), 8*len(labels)), true
	}
	if rest, ok := strings.CutSuffix(name, ".ip6.arpa"); ok {
		labels := strings.Split(rest, ".")
		if len(labels) > 32 {
			return netip.Prefix{}, false
		}
		var ip [16]byte
		for i, l := range labels {
			v, err := strconv.ParseUint(l, 16, 8)
			if err != nil || len(l) != 1 {
				return netip.Prefix{}, false
			}
			n := len(labels) - 1 - i // nibble index from the most significant
			ip[n/2] |= byte(v) << (4 * (1 - n%2))
		}
		return netip.PrefixFrom(netip.AddrFrom16(ip), 4*len(labels)), true
	}
	return netip.Prefix{}, false
}
