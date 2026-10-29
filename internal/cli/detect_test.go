package cli

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/KadirBerkpolat1/SplitDPI/internal/i18n"
)

type failTransport struct{ t *testing.T }

func (f failTransport) RoundTrip(*http.Request) (*http.Response, error) {
	f.t.Fatal("unexpected HTTP request")
	return nil, nil
}

func TestDetectASNNoHTTP(t *testing.T) {
	i18n.Init("en")
	o := rootOps(t)
	o.HTTP = &http.Client{Transport: failTransport{t: t}}
	out, code := runOps(t, o, "detect", "--asn", "9121")
	if code != 0 {
		t.Fatalf("code %d, out %q", code, out)
	}
	has(t, out, "Türk Telekom")
}

func TestDetectAsksHost(t *testing.T) {
	i18n.Init("en")
	o := rootOps(t)
	o.HTTP = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"org":"AS9121 Turk Telekom","country":"TR"}`)),
			Header:     http.Header{"Content-Type": []string{"application/json"}},
		}, nil
	})}
	out, code := runOps(t, o, "detect")
	if code != 0 {
		t.Fatalf("code %d out %q", code, out)
	}
	ask := strings.Index(out, "ipinfo.io")
	if ask < 0 {
		t.Fatalf("out %q should mention ipinfo.io", out)
	}
	if res := strings.Index(out, "9121"); res >= 0 && res < ask {
		t.Fatalf("asking line should precede result")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
