package dns_test

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"golang.org/x/net/dns/dnsmessage"

	"github.com/KadirBerkpolat1/SplitDPI/internal/dns"
)

// names records the question names a fake server was asked.
type names struct {
	mu   sync.Mutex
	seen []string
}

func (n *names) add(s string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.seen = append(n.seen, s)
}

func (n *names) list() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]string(nil), n.seen...)
}

// replyA answers q's A question with a, or with n copies of it when n > 1.
func replyA(q dnsmessage.Message, a [4]byte, n int) []byte {
	resp := dnsmessage.Message{Header: dnsmessage.Header{ID: q.ID, Response: true, RecursionAvailable: true}, Questions: q.Questions}
	for range max(n, 1) {
		resp.Answers = append(resp.Answers, dnsmessage.Resource{
			Header: dnsmessage.ResourceHeader{Name: q.Questions[0].Name, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET, TTL: 60},
			Body:   &dnsmessage.AResource{A: a},
		})
	}
	b, _ := resp.Pack()
	return b
}

// recordingDoH is the encrypted provider: it answers 192.0.2.7 and records names.
func recordingDoH(t *testing.T) (*httptest.Server, *names) {
	t.Helper()
	seen := &names{}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var q dnsmessage.Message
		if q.Unpack(body) != nil || len(q.Questions) == 0 {
			http.Error(w, "bad", http.StatusBadRequest)
			return
		}
		seen.add(q.Questions[0].Name.String())
		w.Header().Set("Content-Type", "application/dns-message")
		w.Write(replyA(q, [4]byte{192, 0, 2, 7}, 1))
	}))
	t.Cleanup(srv.Close)
	return srv, seen
}

// plainDNS is a router's DNS server: it answers a over UDP and TCP. Over UDP
// it sets TC for names in big, which then get 60 answers over TCP.
type plainDNS struct {
	udp, tcp string
	seen     names
}

func newPlainDNS(t *testing.T, a [4]byte, big ...string) *plainDNS {
	t.Helper()
	uc, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	tl, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { uc.Close(); tl.Close() })
	s := &plainDNS{udp: uc.LocalAddr().String(), tcp: tl.Addr().String()}
	isBig := func(q dnsmessage.Message) bool {
		for _, b := range big {
			if q.Questions[0].Name.String() == b {
				return true
			}
		}
		return false
	}
	go func() {
		buf := make([]byte, 65535)
		for {
			n, addr, err := uc.ReadFrom(buf)
			if err != nil {
				return
			}
			var q dnsmessage.Message
			if q.Unpack(buf[:n]) != nil || len(q.Questions) == 0 {
				continue
			}
			s.seen.add("udp " + q.Questions[0].Name.String())
			if isBig(q) {
				tc := dnsmessage.Message{Header: dnsmessage.Header{ID: q.ID, Response: true, Truncated: true}, Questions: q.Questions}
				b, _ := tc.Pack()
				uc.WriteTo(b, addr)
				continue
			}
			uc.WriteTo(replyA(q, a, 1), addr)
		}
	}()
	go func() {
		for {
			c, err := tl.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				var l [2]byte
				if _, err := io.ReadFull(c, l[:]); err != nil {
					return
				}
				b := make([]byte, binary.BigEndian.Uint16(l[:]))
				if _, err := io.ReadFull(c, b); err != nil {
					return
				}
				var q dnsmessage.Message
				if q.Unpack(b) != nil || len(q.Questions) == 0 {
					return
				}
				s.seen.add("tcp " + q.Questions[0].Name.String())
				n := 1
				if isBig(q) {
					n = 60
				}
				resp := replyA(q, a, n)
				c.Write(append(binary.BigEndian.AppendUint16(nil, uint16(len(resp))), resp...))
			}()
		}
	}()
	return s
}

// dialer sends plain DNS for each "ip:53" in servers to its fake; anything
// else is an error. Tests run unprivileged, so the sockets are unmarked.
func dialer(servers map[string]*plainDNS) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		s, ok := servers[addr]
		if !ok {
			return nil, errors.New("no fake server at " + addr)
		}
		target := s.udp
		if network == "tcp" {
			target = s.tcp
		}
		var d net.Dialer
		return d.DialContext(ctx, network, target)
	}
}

func startLocal(t *testing.T, up *httptest.Server, local *dns.Local) *dns.Forwarder {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	f := &dns.Forwarder{DNS: &dns.Client{Upstreams: dohUpstreams(t, up.URL), HTTP: up.Client()}, Local: local}
	if err := f.Start(ctx); err != nil {
		t.Fatal(err)
	}
	return f
}

func queryName(t *testing.T, id uint16, name string) []byte {
	t.Helper()
	q := dnsmessage.Message{
		Header:    dnsmessage.Header{ID: id, RecursionDesired: true},
		Questions: []dnsmessage.Question{{Name: dnsmessage.MustNewName(name), Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET}},
	}
	b, err := q.Pack()
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func resolvRoot(conf string) fstest.MapFS {
	return fstest.MapFS{
		"etc/resolv.conf":                 &fstest.MapFile{Data: []byte("nameserver 127.0.0.53\nsearch corp.example\n")},
		"run/systemd/resolve/resolv.conf": &fstest.MapFile{Data: []byte(conf)},
	}
}

func TestLocalNamesGoToLocalServer(t *testing.T) {
	up, upSeen := recordingDoH(t)
	router := newPlainDNS(t, [4]byte{192, 168, 1, 50}, "big.lan.")
	f := startLocal(t, up, &dns.Local{
		Root: resolvRoot("nameserver 192.168.1.1\n"),
		Dial: dialer(map[string]*plainDNS{"192.168.1.1:53": router}),
	})

	if m := udp(t, f.Port, queryName(t, 0x1111, "printer.lan.")); m.ID != 0x1111 || answerA(t, m) != "192.168.1.50" {
		t.Errorf("printer.lan: id %#x, %s", m.ID, answerA(t, m))
	}
	if m := tcp(t, f.Port, queryName(t, 0x2222, "git.corp.example.")); answerA(t, m) != "192.168.1.50" {
		t.Errorf("search domain: %s", answerA(t, m))
	}
	if m := udp(t, f.Port, queryName(t, 0x3333, "example.com.")); answerA(t, m) != "192.0.2.7" {
		t.Errorf("example.com: %s", answerA(t, m))
	}
	// Truncated over UDP from the router: retried over TCP, then fitted for our client.
	if m := tcp(t, f.Port, queryName(t, 0x4444, "big.lan.")); m.Truncated || len(m.Answers) != 60 {
		t.Errorf("big.lan over tcp: truncated %v, %d answers", m.Truncated, len(m.Answers))
	}

	if got := upSeen.list(); len(got) != 1 || got[0] != "example.com." {
		t.Errorf("encrypted provider asked %v, want only example.com.", got)
	}
	want := map[string]bool{"udp printer.lan.": true, "udp git.corp.example.": true, "udp big.lan.": true, "tcp big.lan.": true}
	got := router.seen.list()
	for _, s := range got {
		if !want[s] {
			t.Errorf("router asked %q", s)
		}
		delete(want, s)
	}
	if len(want) != 0 {
		t.Errorf("router never asked %v (asked %v)", want, got)
	}
}

func TestLocalNameWithoutLocalServerFails(t *testing.T) {
	up, upSeen := recordingDoH(t)
	f := startLocal(t, up, &dns.Local{
		Root: resolvRoot("nameserver 127.0.0.1\n"), // only loopback, and no default route
		Dial: dialer(nil),
	})
	m := udp(t, f.Port, queryName(t, 0xABCD, "printer.lan."))
	if m.RCode != dnsmessage.RCodeServerFailure || m.ID != 0xABCD {
		t.Errorf("got %+v", m.Header)
	}
	if got := upSeen.list(); len(got) != 0 {
		t.Errorf("local name leaked to the encrypted provider: %v", got)
	}
}

func TestLocalServerFallsBackToNext(t *testing.T) {
	up, _ := recordingDoH(t)
	router := newPlainDNS(t, [4]byte{10, 0, 0, 9})
	f := startLocal(t, up, &dns.Local{
		Root: resolvRoot("nameserver 192.168.1.1\nnameserver 10.0.0.1\n"),
		Dial: dialer(map[string]*plainDNS{"10.0.0.1:53": router}),
	})
	if m := udp(t, f.Port, queryName(t, 1, "nas.")); answerA(t, m) != "10.0.0.9" {
		t.Errorf("got %s", answerA(t, m))
	}
}

// The resolver files are read again 30 s later: the laptop joined another network.
func TestLocalServersReread(t *testing.T) {
	up, _ := recordingDoH(t)
	home := newPlainDNS(t, [4]byte{192, 168, 1, 50})
	office := newPlainDNS(t, [4]byte{10, 1, 1, 50})
	root := resolvRoot("nameserver 192.168.1.1\n")
	var mu sync.Mutex
	now := time.Unix(1000, 0)
	f := startLocal(t, up, &dns.Local{
		Root: root,
		Dial: dialer(map[string]*plainDNS{"192.168.1.1:53": home, "10.1.1.1:53": office}),
		Now: func() time.Time {
			mu.Lock()
			defer mu.Unlock()
			return now
		},
	})
	ask := func(id uint16) string { return answerA(t, udp(t, f.Port, queryName(t, id, "printer.lan."))) }
	if got := ask(1); got != "192.168.1.50" {
		t.Fatalf("home: %s", got)
	}
	root["run/systemd/resolve/resolv.conf"] = &fstest.MapFile{Data: []byte("nameserver 10.1.1.1\n")}
	mu.Lock()
	now = now.Add(29 * time.Second)
	mu.Unlock()
	if got := ask(2); got != "192.168.1.50" {
		t.Errorf("re-read before 30 s: %s", got)
	}
	mu.Lock()
	now = now.Add(time.Second)
	mu.Unlock()
	if got := ask(3); got != "10.1.1.50" {
		t.Errorf("office after 30 s: %s", got)
	}
}
