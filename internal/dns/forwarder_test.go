package dns_test

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"

	"github.com/KadirBerkpolat1/SplitDPI/internal/dns"
)

// doh answers every A question with n copies of 192.0.2.7.
func doh(t *testing.T, n int) *httptest.Server {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var q dnsmessage.Message
		if r.Header.Get("Content-Type") != "application/dns-message" || q.Unpack(body) != nil {
			http.Error(w, "bad", http.StatusBadRequest)
			return
		}
		resp := dnsmessage.Message{Header: dnsmessage.Header{ID: q.ID, Response: true, RecursionAvailable: true}, Questions: q.Questions}
		for range n {
			resp.Answers = append(resp.Answers, dnsmessage.Resource{
				Header: dnsmessage.ResourceHeader{Name: q.Questions[0].Name, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET, TTL: 60},
				Body:   &dnsmessage.AResource{A: [4]byte{192, 0, 2, 7}},
			})
		}
		b, _ := resp.Pack()
		w.Header().Set("Content-Type", "application/dns-message")
		w.Write(b)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func start(t *testing.T, client *http.Client, upstreams ...string) *dns.Forwarder {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	f := &dns.Forwarder{Upstreams: upstreams, Client: client, Port: 0}
	if err := f.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if f.Port == 0 {
		t.Fatal("ephemeral port not recorded")
	}
	return f
}

func query(t *testing.T, id uint16) []byte {
	t.Helper()
	q := dnsmessage.Message{
		Header:    dnsmessage.Header{ID: id, RecursionDesired: true},
		Questions: []dnsmessage.Question{{Name: dnsmessage.MustNewName("discord.com."), Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET}},
	}
	b, err := q.Pack()
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func udp(t *testing.T, port int, q []byte) dnsmessage.Message {
	t.Helper()
	c, err := net.Dial("udp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(10 * time.Second))
	if _, err := c.Write(q); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 65535)
	n, err := c.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	var m dnsmessage.Message
	if err := m.Unpack(buf[:n]); err != nil {
		t.Fatal(err)
	}
	return m
}

func tcp(t *testing.T, port int, q []byte) dnsmessage.Message {
	t.Helper()
	c, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(10 * time.Second))
	c.Write(binary.BigEndian.AppendUint16(nil, uint16(len(q))))
	c.Write(q)
	var l [2]byte
	if _, err := io.ReadFull(c, l[:]); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, binary.BigEndian.Uint16(l[:]))
	if _, err := io.ReadFull(c, buf); err != nil {
		t.Fatal(err)
	}
	var m dnsmessage.Message
	if err := m.Unpack(buf); err != nil {
		t.Fatal(err)
	}
	return m
}

func answerA(t *testing.T, m dnsmessage.Message) string {
	t.Helper()
	if len(m.Answers) == 0 {
		t.Fatalf("no answers: %+v", m.Header)
	}
	a, ok := m.Answers[0].Body.(*dnsmessage.AResource)
	if !ok {
		t.Fatalf("answer %T", m.Answers[0].Body)
	}
	return net.IP(a.A[:]).String()
}

func TestRelayUDP(t *testing.T) {
	up := doh(t, 1)
	f := start(t, up.Client(), up.URL)
	m := udp(t, f.Port, query(t, 0x1234))
	if m.ID != 0x1234 || answerA(t, m) != "192.0.2.7" {
		t.Errorf("got id %#x, %+v", m.ID, m.Answers)
	}
}

func TestRelayTCP(t *testing.T) {
	up := doh(t, 1)
	f := start(t, up.Client(), up.URL)
	if m := tcp(t, f.Port, query(t, 7)); m.ID != 7 || answerA(t, m) != "192.0.2.7" {
		t.Errorf("got id %d, %+v", m.ID, m.Answers)
	}
}

func TestFallsBackToNextUpstream(t *testing.T) {
	dead := doh(t, 1)
	deadURL := dead.URL
	dead.Close()
	up := doh(t, 1)
	f := start(t, up.Client(), deadURL, up.URL)
	if got := answerA(t, udp(t, f.Port, query(t, 1))); got != "192.0.2.7" {
		t.Errorf("got %s", got)
	}
}

func TestAllUpstreamsFailServfail(t *testing.T) {
	bad := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "down", http.StatusInternalServerError)
	}))
	defer bad.Close()
	f := start(t, bad.Client(), bad.URL, bad.URL)
	m := udp(t, f.Port, query(t, 0xBEEF))
	if m.RCode != dnsmessage.RCodeServerFailure || m.ID != 0xBEEF || !m.Response || len(m.Questions) != 1 {
		t.Errorf("got %+v, questions %v", m.Header, m.Questions)
	}
}

// A plain (non-EDNS) client may only take 512 bytes over UDP; it must be told
// to retry over TCP, which gets the whole answer.
func TestTruncatesLargeUDPAnswer(t *testing.T) {
	up := doh(t, 60)
	f := start(t, up.Client(), up.URL)
	m := udp(t, f.Port, query(t, 2))
	if !m.Truncated {
		t.Errorf("large UDP answer not truncated: %d answers", len(m.Answers))
	}
	if m := tcp(t, f.Port, query(t, 3)); m.Truncated || len(m.Answers) != 60 {
		t.Errorf("tcp: truncated %v, %d answers", m.Truncated, len(m.Answers))
	}
}

func TestStopsOnCancel(t *testing.T) {
	up := doh(t, 1)
	ctx, cancel := context.WithCancel(context.Background())
	f := &dns.Forwarder{Upstreams: []string{up.URL}, Client: up.Client()}
	if err := f.Start(ctx); err != nil {
		t.Fatal(err)
	}
	cancel()
	addr := fmt.Sprintf("127.0.0.1:%d", f.Port)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		c, err := net.Dial("tcp", addr)
		if err != nil {
			return
		}
		c.Close()
		time.Sleep(20 * time.Millisecond)
	}
	t.Error("still listening after cancel")
}
