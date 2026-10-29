package probe_test

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"net/url"
	"os"
	"syscall"
	"testing"

	"github.com/KadirBerkpolat1/SplitDPI/internal/probe"
)

// lookups answers each host from a table and counts the questions.
type lookups struct {
	answers map[string][]netip.Addr
	err     error
	asked   int
}

func (l *lookups) lookup(_ context.Context, host string) ([]netip.Addr, error) {
	l.asked++
	return l.answers[host], l.err
}

func notFound(host string) error {
	return asHTTP("dial", &net.DNSError{Err: "no such host", Name: host, IsNotFound: true})
}

// refusedAt is the error of a connection to ip that nothing accepted.
func refusedAt(ip string) error {
	return &url.Error{Op: "Get", URL: "https://example.net/", Err: &net.OpError{Op: "dial", Net: "tcp", Addr: &net.TCPAddr{IP: net.ParseIP(ip), Port: 443}, Err: os.NewSyscallError("connect", syscall.ECONNREFUSED)}}
}

func TestMarkNoAddress(t *testing.T) {
	loop := []netip.Addr{addr("127.0.0.1")}
	public := []netip.Addr{addr("104.16.0.1")}
	cases := []struct {
		name     string
		cause    error
		sys, doh []netip.Addr
		sysErr   error
		want     probe.Category
		asked    bool
	}{
		{name: "nxdomain, DoH none", cause: notFound("roblox.info"), want: probe.NoAddress, asked: true},
		{name: "nodata", cause: notFound("rbxcdn.com"), sys: []netip.Addr{}, doh: []netip.Addr{}, want: probe.NoAddress, asked: true},
		{name: "loopback answer", cause: refusedAt("127.0.0.1"), sys: loop, doh: loop, want: probe.NoAddress, asked: true},
		{name: "unspecified answer", cause: refusedAt("0.0.0.0"), sys: []netip.Addr{addr("0.0.0.0"), addr("::")}, want: probe.NoAddress, asked: true},
		{name: "system none, DoH has address", cause: notFound("discord.com"), doh: public, want: probe.DNS, asked: true},
		{name: "system cannot tell", cause: notFound("discord.com"), sysErr: errors.New("i/o timeout"), want: probe.DNS, asked: true},
		{name: "loopback, DoH has address", cause: refusedAt("127.0.0.1"), sys: loop, doh: public, want: probe.Other, asked: true},
		{name: "refused elsewhere", cause: refusedAt("104.16.0.1"), want: probe.Other},
		{name: "reset", cause: asHTTP("read", &os.SyscallError{Syscall: "read", Err: syscall.ECONNRESET}), want: probe.Reset},
	}
	for _, c := range cases {
		const host = "example.net"
		sys := &lookups{answers: map[string][]netip.Addr{host: c.sys}, err: c.sysErr}
		doh := &lookups{answers: map[string][]netip.Addr{host: c.doh}}
		rs := []probe.Result{{URL: "https://" + host + "/", Cause: c.cause}, {URL: "https://ok.example/", OK: true}}
		probe.MarkNoAddress(context.Background(), rs, sys.lookup, doh.lookup)
		if got := probe.Classify(rs[0]); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
		if (sys.asked > 0) != c.asked || sys.asked > 1 {
			t.Errorf("%s: system asked %d times", c.name, sys.asked)
		}
		if c.sysErr != nil && doh.asked > 0 {
			t.Errorf("%s: DoH asked after the system could not tell", c.name)
		}
		if probe.Classify(rs[1]) != probe.None {
			t.Errorf("%s: passing result changed: %+v", c.name, rs[1])
		}
	}
}

func TestMarkNoAddressOncePerHost(t *testing.T) {
	sys, doh := &lookups{}, &lookups{}
	rs := []probe.Result{
		{URL: "https://phncdn.com/", Cause: notFound("phncdn.com")},
		{URL: "https://phncdn.com/x", Cause: notFound("phncdn.com")},
	}
	probe.MarkNoAddress(context.Background(), rs, sys.lookup, doh.lookup)
	if sys.asked != 1 || doh.asked != 1 || !rs[0].NoAddress || !rs[1].NoAddress {
		t.Fatalf("asked %d/%d, results %+v", sys.asked, doh.asked, rs)
	}
}

func TestOverall(t *testing.T) {
	ok := probe.Result{OK: true}
	none := probe.Result{NoAddress: true}
	reset := probe.Result{Cause: asHTTP("read", &os.SyscallError{Syscall: "read", Err: syscall.ECONNRESET})}
	for _, c := range []struct {
		rs   []probe.Result
		want probe.Category
	}{
		{nil, probe.None},
		{[]probe.Result{ok}, probe.None},
		{[]probe.Result{none}, probe.NoAddress},
		{[]probe.Result{none, ok}, probe.None},
		{[]probe.Result{ok, none, reset}, probe.Reset},
	} {
		if got := probe.Overall(c.rs); got != c.want {
			t.Errorf("%+v: got %q, want %q", c.rs, got, c.want)
		}
	}
	if probe.NoAddress.Failed() || probe.None.Failed() || !probe.Reset.Failed() {
		t.Error("Failed")
	}
}
