// Package dns is the opt-in local DNS-over-HTTPS forwarder.
// nft redirects outbound port 53 to it; it never touches resolver config.
package dns

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/http"
	"time"

	"golang.org/x/net/dns/dnsmessage"

	"github.com/KadirBerkpolat1/SplitDPI/internal/probe"
)

// Port is where the nft redirect sends port-53 traffic.
const Port = 5453

const (
	upstreamTimeout = 2 * time.Second
	tcpIdle         = 10 * time.Second
	plainUDPLimit   = 512 // RFC 1035, for clients without EDNS
)

type Forwarder struct {
	Upstreams []string // DoH URLs, tried in order
	Client    *http.Client
	Port      int // 0 picks a free port and records it here
}

// Start listens on UDP and TCP at 127.0.0.1:Port and, if IPv6 is up,
// [::1]:Port, then serves in the background until ctx is done. It returns
// once the listeners are bound, so traffic can be redirected right away.
func (f *Forwarder) Start(ctx context.Context) error {
	u4, t4, err := f.listen4()
	if err != nil {
		return err
	}
	udps, tcps := []*net.UDPConn{u4}, []*net.TCPListener{t4}
	// ::1 is optional: IPv6 may be disabled, and IPv4 then carries all DNS.
	if u6, err := net.ListenUDP("udp6", &net.UDPAddr{IP: net.IPv6loopback, Port: f.Port}); err == nil {
		if t6, err := net.ListenTCP("tcp6", &net.TCPAddr{IP: net.IPv6loopback, Port: f.Port}); err == nil {
			udps, tcps = append(udps, u6), append(tcps, t6)
		} else {
			u6.Close()
		}
	}
	go func() {
		<-ctx.Done()
		for _, c := range udps {
			c.Close()
		}
		for _, l := range tcps {
			l.Close()
		}
	}()
	for _, c := range udps {
		go f.serveUDP(ctx, c)
	}
	for _, l := range tcps {
		go f.serveTCP(ctx, l)
	}
	return nil
}

// listen4 binds UDP and TCP on 127.0.0.1:Port. With Port 0 the UDP port is
// picked first and its number may already be taken for TCP, so it retries.
func (f *Forwarder) listen4() (*net.UDPConn, *net.TCPListener, error) {
	pick := f.Port == 0
	for attempt := 0; ; attempt++ {
		u, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: f.Port})
		if err != nil {
			return nil, nil, err
		}
		port := u.LocalAddr().(*net.UDPAddr).Port
		t, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: port})
		if err == nil {
			f.Port = port
			return u, t, nil
		}
		u.Close()
		if !pick || attempt == 9 {
			return nil, nil, err
		}
	}
}

func (f *Forwarder) serveUDP(ctx context.Context, c *net.UDPConn) {
	buf := make([]byte, 65535)
	for {
		n, addr, err := c.ReadFromUDP(buf)
		if errors.Is(err, net.ErrClosed) {
			return
		}
		if err != nil {
			continue
		}
		q := append([]byte(nil), buf[:n]...)
		go func() {
			if resp := f.answer(ctx, q); resp != nil {
				c.WriteToUDP(fitUDP(q, resp), addr)
			}
		}()
	}
}

func (f *Forwarder) serveTCP(ctx context.Context, l *net.TCPListener) {
	for {
		c, err := l.Accept()
		if errors.Is(err, net.ErrClosed) {
			return
		}
		if err != nil {
			continue
		}
		go f.serveConn(ctx, c)
	}
}

// serveConn answers length-prefixed queries (RFC 1035 4.2.2) until the client goes idle.
func (f *Forwarder) serveConn(ctx context.Context, c net.Conn) {
	defer c.Close()
	for {
		c.SetDeadline(time.Now().Add(tcpIdle))
		var l [2]byte
		if _, err := io.ReadFull(c, l[:]); err != nil {
			return
		}
		q := make([]byte, binary.BigEndian.Uint16(l[:]))
		if _, err := io.ReadFull(c, q); err != nil {
			return
		}
		resp := f.answer(ctx, q)
		if resp == nil {
			return
		}
		if _, err := c.Write(append(binary.BigEndian.AppendUint16(nil, uint16(len(resp))), resp...)); err != nil {
			return
		}
	}
}

// answer relays q to the first upstream that answers within 2 s, or returns
// SERVFAIL. It returns nil for input that is not a DNS query.
func (f *Forwarder) answer(ctx context.Context, q []byte) []byte {
	var p dnsmessage.Parser
	h, err := p.Start(q)
	if err != nil || h.Response {
		return nil
	}
	for _, up := range f.Upstreams {
		uctx, cancel := context.WithTimeout(ctx, upstreamTimeout)
		resp, err := probe.DoHExchange(uctx, f.Client, up, q)
		cancel()
		var rp dnsmessage.Parser
		if err != nil {
			continue
		}
		if _, err := rp.Start(resp); err != nil {
			continue
		}
		// DoH servers may zero the ID (RFC 8484 4.1); the client needs its own back.
		binary.BigEndian.PutUint16(resp, h.ID)
		return resp
	}
	qs, _ := p.AllQuestions()
	fail := dnsmessage.Message{
		Header: dnsmessage.Header{
			ID: h.ID, Response: true, OpCode: h.OpCode, RecursionDesired: h.RecursionDesired,
			RecursionAvailable: true, RCode: dnsmessage.RCodeServerFailure,
		},
		Questions: qs,
	}
	b, err := fail.Pack()
	if err != nil {
		return nil
	}
	return b
}

// fitUDP truncates resp (TC bit, no records) when it exceeds what the client
// accepts over UDP, so the client retries over TCP.
func fitUDP(q, resp []byte) []byte {
	if len(resp) <= udpLimit(q) {
		return resp
	}
	var m dnsmessage.Message
	if err := m.Unpack(resp); err != nil {
		return resp
	}
	m.Truncated = true
	m.Answers, m.Authorities, m.Additionals = nil, nil, nil
	b, err := m.Pack()
	if err != nil {
		return resp
	}
	return b
}

// udpLimit is the client's EDNS UDP payload size, or 512 without EDNS.
func udpLimit(q []byte) int {
	var p dnsmessage.Parser
	if _, err := p.Start(q); err != nil {
		return plainUDPLimit
	}
	if p.SkipAllQuestions() != nil || p.SkipAllAnswers() != nil || p.SkipAllAuthorities() != nil {
		return plainUDPLimit
	}
	for {
		h, err := p.AdditionalHeader()
		if err != nil {
			return plainUDPLimit
		}
		if h.Type == dnsmessage.TypeOPT {
			return max(int(h.Class), plainUDPLimit)
		}
		if p.SkipAdditional() != nil {
			return plainUDPLimit
		}
	}
}
