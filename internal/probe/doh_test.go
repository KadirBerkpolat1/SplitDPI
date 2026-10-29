package probe_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"

	"github.com/KadirBerkpolat1/SplitDPI/internal/probe"
	"golang.org/x/net/dns/dnsmessage"
)

func packA(name, ip string) []byte {
	var msg dnsmessage.Message
	msg.Header.Response = true
	msg.Header.ID = 1
	msg.Questions = []dnsmessage.Question{{
		Name:  dnsmessage.MustNewName(name + "."),
		Type:  dnsmessage.TypeA,
		Class: dnsmessage.ClassINET,
	}}
	msg.Answers = []dnsmessage.Resource{{
		Header: dnsmessage.ResourceHeader{
			Name:  dnsmessage.MustNewName(name + "."),
			Type:  dnsmessage.TypeA,
			Class: dnsmessage.ClassINET,
		},
		Body: &dnsmessage.AResource{A: [4]byte{1, 2, 3, 4}},
	}}
	b, err := msg.Pack()
	if err != nil {
		panic(err)
	}
	return b
}

func TestDoHParseResponse(t *testing.T) {
	body := packA("example.com", "1.2.3.4")
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept") != "application/dns-message" {
			t.Errorf("accept %q", r.Header.Get("Accept"))
		}
		w.Header().Set("Content-Type", "application/dns-message")
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	addrs, err := probe.DoH(context.Background(), srv.Client(), srv.URL, "example.com")
	if err != nil {
		t.Fatal(err)
	}
	if len(addrs) != 1 || addrs[0] != netip.MustParseAddr("1.2.3.4") {
		t.Fatalf("got %v", addrs)
	}
}
