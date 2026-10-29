package dns_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"slices"
	"sync/atomic"
	"testing"

	"golang.org/x/net/dns/dnsmessage"

	"github.com/KadirBerkpolat1/SplitDPI/internal/dns"
)

var (
	wantA    = netip.MustParseAddr("192.0.2.53")
	wantAAAA = netip.MustParseAddr("2001:db8::53")
)

// reply answers A with wantA and AAAA with wantAAAA.
func reply(t *testing.T, q []byte) []byte {
	var m dnsmessage.Message
	if err := m.Unpack(q); err != nil || len(m.Questions) != 1 {
		t.Errorf("bad query: %v, %d questions", err, len(m.Questions))
		return nil
	}
	qq := m.Questions[0]
	resp := dnsmessage.Message{Header: dnsmessage.Header{ID: m.ID, Response: true}, Questions: m.Questions}
	h := dnsmessage.ResourceHeader{Name: qq.Name, Type: qq.Type, Class: dnsmessage.ClassINET, TTL: 60}
	switch qq.Type {
	case dnsmessage.TypeA:
		resp.Answers = []dnsmessage.Resource{{Header: h, Body: &dnsmessage.AResource{A: wantA.As4()}}}
	case dnsmessage.TypeAAAA:
		resp.Answers = []dnsmessage.Resource{{Header: h, Body: &dnsmessage.AAAAResource{AAAA: wantAAAA.As16()}}}
	}
	b, err := resp.Pack()
	if err != nil {
		t.Error(err)
	}
	return b
}

type dotServer struct {
	addr    netip.AddrPort
	roots   *x509.CertPool
	accepts atomic.Int32
}

// dot serves DoT on 127.0.0.1 with a certificate for that IP. With hangUp it
// closes each connection after one answer, as servers do with idle ones.
func dot(t *testing.T, hangUp bool) *dotServer {
	t.Helper()
	ref := httptest.NewTLSServer(http.NotFoundHandler())
	t.Cleanup(ref.Close)
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: ref.TLS.Certificates})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	s := &dotServer{addr: netip.MustParseAddrPort(ln.Addr().String()), roots: x509.NewCertPool()}
	s.roots.AddCert(ref.Certificate())
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			s.accepts.Add(1)
			go func() {
				defer c.Close()
				for {
					var l [2]byte
					if _, err := io.ReadFull(c, l[:]); err != nil {
						return
					}
					q := make([]byte, binary.BigEndian.Uint16(l[:]))
					if _, err := io.ReadFull(c, q); err != nil {
						return
					}
					resp := reply(t, q)
					c.Write(append(binary.BigEndian.AppendUint16(nil, uint16(len(resp))), resp...))
					if hangUp {
						return
					}
				}
			}()
		}
	}()
	return s
}

func (s *dotServer) client() *dns.Client {
	return &dns.Client{
		Upstreams: []dns.Upstream{{Protocol: dns.DoT, Addr: s.addr}},
		TLS:       &tls.Config{RootCAs: s.roots},
	}
}

func TestDoTResolveReusesConnection(t *testing.T) {
	s := dot(t, false)
	c := s.client()
	defer c.CloseIdle()
	got, err := c.Resolve(context.Background(), "discord.com")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, []netip.Addr{wantA, wantAAAA}) {
		t.Errorf("got %v", got)
	}
	if n := s.accepts.Load(); n != 1 {
		t.Errorf("%d connections for two queries, want 1", n)
	}
}

func TestDoTRedialsClosedConnection(t *testing.T) {
	s := dot(t, true)
	c := s.client()
	defer c.CloseIdle()
	for i := range 2 {
		if _, err := c.Resolve(context.Background(), "discord.com"); err != nil {
			t.Fatalf("query %d: %v", i, err)
		}
	}
}

// The certificate must be verified: the test CA is not a system root.
func TestDoTVerifiesCertificate(t *testing.T) {
	s := dot(t, false)
	c := &dns.Client{Upstreams: []dns.Upstream{{Protocol: dns.DoT, Addr: s.addr}}}
	if _, err := c.Resolve(context.Background(), "discord.com"); err == nil {
		t.Fatal("untrusted DoT certificate accepted")
	}
}

func dohServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/dns-message" || r.URL.Path != "/dns-query" {
			http.Error(w, "bad", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/dns-message")
		w.Write(reply(t, body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestDoHResolve(t *testing.T) {
	srv := dohServer(t)
	c := &dns.Client{Upstreams: dohUpstreams(t, srv.URL), HTTP: srv.Client()}
	got, err := c.Resolve(context.Background(), "discord.com")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, []netip.Addr{wantA, wantAAAA}) {
		t.Errorf("got %v", got)
	}
}

// A dead DoT server and one answering garbage are skipped for the next upstream.
func TestExchangeFallsBack(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	dead := netip.MustParseAddrPort(ln.Addr().String())
	ln.Close()
	junk := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("not dns"))
	}))
	defer junk.Close()
	s := dot(t, false)
	c := s.client()
	defer c.CloseIdle()
	c.Upstreams = append([]dns.Upstream{{Protocol: dns.DoT, Addr: dead}, dohUpstreams(t, junk.URL)[0]}, c.Upstreams...)
	c.HTTP = junk.Client()
	got, err := c.Resolve(context.Background(), "discord.com")
	if err != nil || len(got) != 2 {
		t.Fatalf("got %v, %v", got, err)
	}
}

// Only a definite answer without addresses is ErrNoAddress; a server failure
// says nothing about the name.
func TestResolveNoAddress(t *testing.T) {
	for _, tc := range []struct {
		name  string
		rcode dnsmessage.RCode
		want  bool
	}{
		{"nxdomain", dnsmessage.RCodeNameError, true},
		{"nodata", dnsmessage.RCodeSuccess, true},
		{"servfail", dnsmessage.RCodeServerFailure, false},
	} {
		srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			var q dnsmessage.Message
			if err := q.Unpack(body); err != nil {
				http.Error(w, "bad", http.StatusBadRequest)
				return
			}
			b, _ := (&dnsmessage.Message{Header: dnsmessage.Header{ID: q.ID, Response: true, RCode: tc.rcode}, Questions: q.Questions}).Pack()
			w.Write(b)
		}))
		c := &dns.Client{Upstreams: dohUpstreams(t, srv.URL), HTTP: srv.Client()}
		got, err := c.Resolve(context.Background(), "rbxcdn.com")
		srv.Close()
		if got != nil || err == nil || errors.Is(err, dns.ErrNoAddress) != tc.want {
			t.Errorf("%s: got %v, %v; want ErrNoAddress %v", tc.name, got, err, tc.want)
		}
	}
}

func TestUpstreamURL(t *testing.T) {
	for addr, want := range map[string]string{
		"1.1.1.1:443":    "https://1.1.1.1/dns-query",
		"127.0.0.1:8443": "https://127.0.0.1:8443/dns-query",
	} {
		if got := (dns.Upstream{Protocol: dns.DoH, Addr: netip.MustParseAddrPort(addr)}).URL(); got != want {
			t.Errorf("%s: %s, want %s", addr, got, want)
		}
	}
}
