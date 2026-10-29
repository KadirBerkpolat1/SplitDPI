package probe_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"syscall"
	"testing"

	"github.com/KadirBerkpolat1/SplitDPI/internal/i18n"
	"github.com/KadirBerkpolat1/SplitDPI/internal/probe"
)

// asHTTP wraps err the way http.Client.Do returns a dial or read failure.
func asHTTP(op string, err error) error {
	return &url.Error{Op: "Get", URL: "https://discord.com/", Err: &net.OpError{Op: op, Net: "tcp", Err: err}}
}

func TestClassify(t *testing.T) {
	hostErr := x509.HostnameError{Certificate: &x509.Certificate{DNSNames: []string{"blockpage.example"}}, Host: "discord.com"}
	cases := []struct {
		name string
		err  error
		want probe.Category
	}{
		{"reset", asHTTP("read", &os.SyscallError{Syscall: "read", Err: syscall.ECONNRESET}), probe.Reset},
		{"aborted", asHTTP("read", syscall.ECONNABORTED), probe.Reset},
		{"eof in handshake", &url.Error{Op: "Get", URL: "https://discord.com/", Err: io.EOF}, probe.Reset},
		{"deadline", &url.Error{Op: "Get", URL: "https://discord.com/", Err: context.DeadlineExceeded}, probe.Timeout},
		{"io timeout", asHTTP("read", os.ErrDeadlineExceeded), probe.Timeout},
		{"no such host", asHTTP("dial", &net.DNSError{Err: "no such host", Name: "discord.com", IsNotFound: true}), probe.DNS},
		{"dns timeout", asHTTP("dial", &net.DNSError{Err: "i/o timeout", Name: "discord.com", IsTimeout: true}), probe.DNS},
		{"hostname mismatch", &url.Error{Op: "Get", URL: "https://discord.com/", Err: &tls.CertificateVerificationError{Err: hostErr}}, probe.TLS},
		{"bare hostname error", hostErr, probe.TLS},
		{"unknown authority", x509.UnknownAuthorityError{}, probe.TLS},
		{"tls alert", &url.Error{Op: "Get", URL: "https://discord.com/", Err: &net.OpError{Op: "remote error", Err: tls.AlertError(40)}}, probe.TLS},
		{"plain http answer", &url.Error{Op: "Get", URL: "https://discord.com/", Err: http.ErrSchemeMismatch}, probe.HTTP},
		{"refused", asHTTP("dial", &os.SyscallError{Syscall: "connect", Err: syscall.ECONNREFUSED}), probe.Other},
		{"canceled", fmt.Errorf("get: %w", context.Canceled), probe.Other},
		{"no cause", nil, probe.Other},
	}
	for _, c := range cases {
		r := probe.Result{URL: "https://discord.com/", Cause: c.err}
		if got := probe.Classify(r); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
	if got := probe.Classify(probe.Result{OK: true, Cause: errors.New("ignored")}); got != probe.None {
		t.Errorf("ok result: got %q", got)
	}
}

func TestCategoryText(t *testing.T) {
	defer i18n.Init("en")
	for _, lang := range []string{"en", "tr"} {
		i18n.Init(lang)
		for _, c := range []probe.Category{probe.Reset, probe.Timeout, probe.DNS, probe.TLS, probe.HTTP, probe.Other, probe.NoAddress} {
			for _, s := range []string{c.What(), c.Todo()} {
				if s == "" || strings.HasPrefix(s, "!") {
					t.Errorf("%s %s: %q", lang, c, s)
				}
			}
		}
	}
}
