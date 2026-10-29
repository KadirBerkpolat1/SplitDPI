// Package domain normalizes and validates hostnames for lists and config.
package domain

import (
	"net"
	"net/netip"
	"net/url"
	"strings"

	"golang.org/x/net/idna"

	"github.com/KadirBerkpolat1/SplitDPI/internal/i18n"
)

// InvalidError is a Normalize failure; Input is what was given.
type InvalidError struct{ Input string }

func (e InvalidError) Error() string {
	if strings.TrimSpace(e.Input) == "" {
		return i18n.T("domain.empty")
	}
	return i18n.T("domain.invalid", e.Input)
}

// Normalize strips a URL-like prefix, wildcard, port and trailing dot, lowercases,
// punycodes IDNs, and validates the result.
func Normalize(in string) (string, error) {
	s := strings.TrimSpace(in)
	if s == "" {
		return "", InvalidError{in}
	}
	if strings.HasPrefix(s, "*.") {
		s = s[2:]
	}
	if strings.Contains(s, "://") {
		u, err := url.Parse(s)
		if err != nil {
			return "", InvalidError{in}
		}
		if u.Hostname() != "" {
			s = u.Hostname()
		}
	} else if i := strings.IndexAny(s, "/?#"); i >= 0 {
		s = s[:i]
	}
	if host, _, err := net.SplitHostPort(s); err == nil {
		s = host
	}
	s = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(s)), ".")
	if s == "" {
		return "", InvalidError{in}
	}
	ascii, err := idna.Lookup.ToASCII(s)
	if err != nil {
		return "", InvalidError{in}
	}
	if !Valid(ascii) {
		return "", InvalidError{in}
	}
	return ascii, nil
}

// Valid reports whether s is a normalized hostname with at least two labels and not an IP.
func Valid(s string) bool {
	if addr, err := netip.ParseAddr(s); err == nil {
		_ = addr
		return false
	}
	if strings.Contains(s, "..") {
		return false
	}
	labels := strings.Split(s, ".")
	if len(labels) < 2 {
		return false
	}
	for _, lab := range labels {
		if lab == "" || len(lab) > 63 {
			return false
		}
	}
	return true
}
