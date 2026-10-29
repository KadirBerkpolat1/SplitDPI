package dns

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"sync"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

const (
	upstreamTimeout = 2 * time.Second
	maxIdleDoT      = 4 // per upstream
)

// Upstream is one encrypted DNS server. Its certificate is verified
// against the IP in Addr, which is dialed directly.
type Upstream struct {
	Protocol Protocol
	Addr     netip.AddrPort
}

// URL is the DoH endpoint of u.
func (u Upstream) URL() string {
	host := u.Addr.String()
	if u.Addr.Port() == 443 {
		host = u.Addr.Addr().String()
	}
	return "https://" + host + "/dns-query"
}

// Client sends DNS messages to Upstreams in order until one answers.
type Client struct {
	Upstreams []Upstream
	HTTP      *http.Client // DoH; nil is http.DefaultClient
	TLS       *tls.Config  // DoT base config; nil uses the system roots

	mu   sync.Mutex
	idle map[netip.AddrPort][]*tls.Conn
}

// Exchange relays one wire-format query to the first upstream that returns a
// well-formed answer within 2 s each.
func (c *Client) Exchange(ctx context.Context, q []byte) ([]byte, error) {
	last := errors.New("no DNS upstreams")
	for _, u := range c.Upstreams {
		uctx, cancel := context.WithTimeout(ctx, upstreamTimeout)
		resp, err := c.exchange(uctx, u, q)
		cancel()
		if err == nil {
			var p dnsmessage.Parser
			if _, err = p.Start(resp); err == nil {
				return resp, nil
			}
		}
		last = fmt.Errorf("%s: %w", u.Addr, err)
	}
	return nil, last
}

// ErrNoAddress means the upstream answered that the name has no A or AAAA
// record: it does not exist, or exists without an address.
var ErrNoAddress = errors.New("no A/AAAA record")

// Resolve looks up the A and AAAA addresses of name. It fails with
// ErrNoAddress only when both questions got a definite answer.
func (c *Client) Resolve(ctx context.Context, name string) ([]netip.Addr, error) {
	fqdn, err := dnsmessage.NewName(name + ".")
	if err != nil {
		return nil, err
	}
	var out []netip.Addr
	var last error
	answered := 0
	// One question per message: many servers answer only the first.
	for _, t := range []dnsmessage.Type{dnsmessage.TypeA, dnsmessage.TypeAAAA} {
		q := dnsmessage.Message{
			Header:    dnsmessage.Header{ID: 0xBEEF, RecursionDesired: true},
			Questions: []dnsmessage.Question{{Name: fqdn, Type: t, Class: dnsmessage.ClassINET}},
		}
		b, err := q.Pack()
		if err != nil {
			return nil, err
		}
		resp, err := c.Exchange(ctx, b)
		if err != nil {
			return nil, err
		}
		var m dnsmessage.Message
		if err := m.Unpack(resp); err != nil {
			last = err
			continue
		}
		if rc := m.Header.RCode; rc == dnsmessage.RCodeSuccess || rc == dnsmessage.RCodeNameError {
			answered++
		} else {
			last = fmt.Errorf("%s: %s", name, rc)
		}
		for _, ans := range m.Answers {
			switch r := ans.Body.(type) {
			case *dnsmessage.AResource:
				out = append(out, netip.AddrFrom4(r.A))
			case *dnsmessage.AAAAResource:
				out = append(out, netip.AddrFrom16(r.AAAA))
			}
		}
	}
	if len(out) == 0 {
		if answered == 2 {
			return nil, fmt.Errorf("%s: %w", name, ErrNoAddress)
		}
		return nil, last
	}
	return out, nil
}

// CloseIdle closes the DoT connections kept for reuse.
func (c *Client) CloseIdle() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, conns := range c.idle {
		for _, conn := range conns {
			conn.Close()
		}
	}
	c.idle = nil
}

func (c *Client) exchange(ctx context.Context, u Upstream, q []byte) ([]byte, error) {
	if u.Protocol == DoT {
		return c.dot(ctx, u, q)
	}
	return c.doh(ctx, u, q)
}

// doh is one RFC 8484 POST.
func (c *Client) doh(ctx context.Context, u Upstream, q []byte) ([]byte, error) {
	hc := c.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.URL(), bytes.NewReader(q))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/dns-message")
	req.Header.Set("Content-Type", "application/dns-message")
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("DoH HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 65535))
}

// dot is one RFC 7858 exchange, on an idle connection when there is one.
func (c *Client) dot(ctx context.Context, u Upstream, q []byte) ([]byte, error) {
	if conn := c.takeIdle(u.Addr); conn != nil {
		if resp, err := tcpRoundTrip(ctx, conn, q); err == nil {
			c.putIdle(u.Addr, conn)
			return resp, nil
		}
		// The server may have closed it while idle; a fresh one decides.
		conn.Close()
	}
	cfg := &tls.Config{}
	if c.TLS != nil {
		cfg = c.TLS.Clone()
	}
	// An IP ServerName sends no SNI and verifies the certificate's IP SAN.
	cfg.ServerName = u.Addr.Addr().String()
	d := tls.Dialer{Config: cfg}
	nc, err := d.DialContext(ctx, "tcp", u.Addr.String())
	if err != nil {
		return nil, err
	}
	conn := nc.(*tls.Conn)
	resp, err := tcpRoundTrip(ctx, conn, q)
	if err != nil {
		conn.Close()
		return nil, err
	}
	c.putIdle(u.Addr, conn)
	return resp, nil
}

// tcpRoundTrip is one length-prefixed exchange (RFC 1035 4.2.2), as DoT uses too.
func tcpRoundTrip(ctx context.Context, conn net.Conn, q []byte) ([]byte, error) {
	if len(q) < 2 {
		return nil, errors.New("short DNS query")
	}
	if dl, ok := ctx.Deadline(); ok {
		conn.SetDeadline(dl)
	}
	stop := context.AfterFunc(ctx, func() { conn.SetDeadline(time.Unix(1, 0)) })
	defer stop()
	if _, err := conn.Write(append(binary.BigEndian.AppendUint16(nil, uint16(len(q))), q...)); err != nil {
		return nil, err
	}
	var l [2]byte
	if _, err := io.ReadFull(conn, l[:]); err != nil {
		return nil, err
	}
	resp := make([]byte, binary.BigEndian.Uint16(l[:]))
	if _, err := io.ReadFull(conn, resp); err != nil {
		return nil, err
	}
	if len(resp) < 2 || !bytes.Equal(resp[:2], q[:2]) {
		return nil, errors.New("DNS answer for another query")
	}
	conn.SetDeadline(time.Time{})
	return resp, nil
}

func (c *Client) takeIdle(a netip.AddrPort) *tls.Conn {
	c.mu.Lock()
	defer c.mu.Unlock()
	conns := c.idle[a]
	if len(conns) == 0 {
		return nil
	}
	conn := conns[len(conns)-1]
	c.idle[a] = conns[:len(conns)-1]
	return conn
}

func (c *Client) putIdle(a netip.AddrPort, conn *tls.Conn) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.idle[a]) >= maxIdleDoT {
		conn.Close()
		return
	}
	if c.idle == nil {
		c.idle = map[netip.AddrPort][]*tls.Conn{}
	}
	c.idle[a] = append(c.idle[a], conn)
}
