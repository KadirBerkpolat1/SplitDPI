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
