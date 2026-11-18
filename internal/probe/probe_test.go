package probe_test

import (
	"context"
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/KadirBerkpolat1/SplitDPI/internal/probe"
)

func TestProbeOK404(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	p := probe.Prober{Client: srv.Client(), Timeout: time.Second}
	res := p.Probe(context.Background(), []string{srv.URL})
	if len(res) != 1 || !res[0].OK {
		t.Fatalf("got %+v", res)
	}
}

func TestProbeTimeout(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(500 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := probe.Prober{Client: srv.Client(), Timeout: 200 * time.Millisecond}
	res := p.Probe(context.Background(), []string{srv.URL})
	if len(res) != 1 || res[0].OK {
		t.Fatalf("got %+v", res)
	}
	if got := probe.Classify(res[0]); got != probe.Timeout {
		t.Fatalf("category %q for %q, want timeout", got, res[0].Err)
	}
}

// Blocked URLs time out together: a failing trial costs one Timeout, not one
// per URL, and the results keep the order of the URLs.
func TestProbeTimeoutsOverlap(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(5 * time.Second):
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()

	p := probe.Prober{Client: srv.Client(), Timeout: 300 * time.Millisecond}
	urls := []string{srv.URL + "/a", srv.URL + "/b", srv.URL + "/c", srv.URL + "/d", srv.URL + "/e"}
	start := time.Now()
	res := p.Probe(context.Background(), urls)
	if d := time.Since(start); d > time.Second {
		t.Errorf("5 blocked URLs took %v, want about one 300ms timeout", d)
	}
	for i, r := range res {
		if r.URL != urls[i] || r.OK {
			t.Errorf("result %d: %+v, want a failed probe of %s", i, r, urls[i])
		}
	}
}

func TestProbeUntrustedTLS(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := probe.Prober{Client: &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12},
	}}, Timeout: time.Second}
	res := p.Probe(context.Background(), []string{srv.URL})
	if len(res) != 1 || res[0].OK {
		t.Fatalf("got %+v", res)
	}
	if got := probe.Classify(res[0]); got != probe.TLS {
		t.Fatalf("category %q for %q, want tls", got, res[0].Err)
	}
}

func TestProbePlainHTTPOnHTTPSPort(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	p := probe.Prober{Timeout: time.Second}
	res := p.Probe(context.Background(), []string{strings.Replace(srv.URL, "http://", "https://", 1)})
	if len(res) != 1 || res[0].OK {
		t.Fatalf("got %+v", res)
	}
	if got := probe.Classify(res[0]); got != probe.HTTP {
		t.Fatalf("category %q for %q, want http", got, res[0].Err)
	}
}

func TestProbeClosedPort(t *testing.T) {
	p := probe.Prober{Client: &http.Client{Timeout: 500 * time.Millisecond}, Timeout: 500 * time.Millisecond}
	res := p.Probe(context.Background(), []string{"https://127.0.0.1:1/"})
	if len(res) != 1 || res[0].OK {
		t.Fatalf("got %+v", res)
	}
}
