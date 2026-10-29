package probe

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/netip"

	"golang.org/x/net/dns/dnsmessage"
)

// DoHUpstreams are IP-based DoH endpoints (no bootstrap DNS needed).
var DoHUpstreams = []string{
	"https://1.1.1.1/dns-query",
	"https://8.8.8.8/dns-query",
	"https://9.9.9.9/dns-query",
}

// KnownBlockIPs are resolver answers that indicate TR-style poisoning.
var KnownBlockIPs = []netip.Addr{netip.MustParseAddr("195.175.254.2")}

// DoH resolves name via one DoH upstream (POST application/dns-message, A+AAAA).
func DoH(ctx context.Context, c *http.Client, upstream, name string) ([]netip.Addr, error) {
	q, err := buildDoHQuery(name)
	if err != nil {
		return nil, err
	}
	body, err := DoHExchange(ctx, c, upstream, q)
	if err != nil {
		return nil, err
	}
	return parseDoHAddrs(body)
}

// DoHExchange sends one wire-format DNS message to upstream (RFC 8484 POST)
// and returns the wire-format answer.
func DoHExchange(ctx context.Context, c *http.Client, upstream string, query []byte) ([]byte, error) {
	if c == nil {
		c = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, upstream, bytes.NewReader(query))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/dns-message")
	req.Header.Set("Content-Type", "application/dns-message")
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("DoH HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 65535))
}

// ResolveDoH tries each DoHUpstreams entry until one succeeds.
func ResolveDoH(ctx context.Context, c *http.Client, name string) ([]netip.Addr, error) {
	var last error
	for _, up := range DoHUpstreams {
		addrs, err := DoH(ctx, c, up, name)
		if err == nil {
			return addrs, nil
		}
		last = err
	}
	if last != nil {
		return nil, last
	}
	return nil, fmt.Errorf("no DoH upstreams")
}

func buildDoHQuery(name string) ([]byte, error) {
	fqdn := dnsmessage.MustNewName(name + ".")
	var msg dnsmessage.Message
	msg.Header.ID = 0xBEEF
	msg.Header.RecursionDesired = true
	msg.Questions = []dnsmessage.Question{
		{Name: fqdn, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET},
		{Name: fqdn, Type: dnsmessage.TypeAAAA, Class: dnsmessage.ClassINET},
	}
	return msg.Pack()
}

func parseDoHAddrs(body []byte) ([]netip.Addr, error) {
	var msg dnsmessage.Message
	if err := msg.Unpack(body); err != nil {
		return nil, err
	}
	var out []netip.Addr
	for _, ans := range msg.Answers {
		switch b := ans.Body.(type) {
		case *dnsmessage.AResource:
			out = append(out, netip.AddrFrom4(b.A))
		case *dnsmessage.AAAAResource:
			out = append(out, netip.AddrFrom16(b.AAAA))
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no A/AAAA in DoH response")
	}
	return out, nil
}
