package probe

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"syscall"

	"github.com/KadirBerkpolat1/SplitDPI/internal/i18n"
)

// Category is the plain-language kind of a failed probe.
type Category string

const (
	None    Category = ""
	Reset   Category = "reset"   // the connection was cut, typically by DPI
	Timeout Category = "timeout" // no answer in time
	DNS     Category = "dns"     // the name did not resolve
	TLS     Category = "tls"     // handshake or certificate failure
	HTTP    Category = "http"    // a plain HTTP answer on the HTTPS port, typically a block page
	Other   Category = "other"
)

// Classify returns the category of a probe result; None when it succeeded.
func Classify(r Result) Category {
	if r.OK {
		return None
	}
	return classifyErr(r.Cause)
}

func classifyErr(err error) Category {
	var (
		dnsErr     *net.DNSError
		hostErr    x509.HostnameError
		authErr    x509.UnknownAuthorityError
		invalidErr x509.CertificateInvalidError
		verifyErr  *tls.CertificateVerificationError
		alertErr   tls.AlertError
		recordErr  tls.RecordHeaderError
		netErr     net.Error
	)
	switch {
	case err == nil:
		return Other
	case errors.As(err, &dnsErr):
		return DNS
	case errors.As(err, &hostErr), errors.As(err, &authErr), errors.As(err, &invalidErr),
		errors.As(err, &verifyErr), errors.As(err, &alertErr), errors.As(err, &recordErr):
		return TLS
	case errors.Is(err, http.ErrSchemeMismatch):
		return HTTP
	case errors.Is(err, syscall.ECONNRESET), errors.Is(err, syscall.ECONNABORTED),
		errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return Reset
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, os.ErrDeadlineExceeded),
		errors.As(err, &netErr) && netErr.Timeout():
		return Timeout
	}
	return Other
}

// What says in plain words what happened.
func (c Category) What() string { return i18n.T("probeerr." + string(c) + ".what") }

// Todo says what the user can do about it.
func (c Category) Todo() string { return i18n.T("probeerr." + string(c) + ".todo") }
