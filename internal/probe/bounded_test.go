package probe_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"slices"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/KadirBerkpolat1/SplitDPI/internal/presets"
	"github.com/KadirBerkpolat1/SplitDPI/internal/probe"
)

func TestAppURLsSkipsUncoveredProbe(t *testing.T) {
	for _, c := range []struct {
		name string
		app  presets.App
		want []string
	}{
		{
			name: "probed host removed",
			app:  presets.App{Hosts: []string{"discord.gg"}, Probe: []string{"https://discord.com/api/v9/gateway"}},
			want: []string{"https://discord.gg/"},
		},
		{
			name: "one of two removed",
			app:  presets.App{Hosts: []string{"protonvpn.com"}, Probe: []string{"https://protonvpn.com/", "https://account.proton.me/"}},
			want: []string{"https://protonvpn.com/"},
		},
		{
			name: "subdomain of a host",
			app:  presets.App{Hosts: []string{"discord.com"}, Probe: []string{"https://gateway.discord.com/"}},
			want: []string{"https://gateway.discord.com/"},
		},
		{
			name: "all hosts removed",
			app:  presets.App{Probe: []string{"https://discord.com/"}},
			want: []string{},
		},
	} {
		if got := probe.AppURLs(c.app); !slices.Equal(got, c.want) {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

// The shipped apps probe only their own hosts, so AppURLs keeps every one.
func TestAppURLsShippedProbesCovered(t *testing.T) {
	for id, a := range presets.Load(presets.Embedded()).Apps {
		if len(a.Probe) > 0 && !slices.Equal(probe.AppURLs(a), a.Probe) {
			t.Errorf("%s: AppURLs %q, probe %q", id, probe.AppURLs(a), a.Probe)
		}
	}
}

// A site that streams after its status costs a small read, not Timeout.
func TestProbeStreamingBodyBounded(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		chunk := make([]byte, 32<<10)
		for r.Context().Err() == nil {
			if _, err := w.Write(chunk); err != nil {
				return
			}
			w.(http.Flusher).Flush()
		}
	}))
	defer srv.Close()

	p := probe.Prober{Client: srv.Client(), Timeout: 3 * time.Second}
	start := time.Now()
	res := p.Probe(context.Background(), []string{srv.URL})
	if d := time.Since(start); d > time.Second {
		t.Errorf("streaming site took %v, want well under the 3s timeout", d)
	}
	if len(res) != 1 || !res[0].OK {
		t.Fatalf("got %+v", res)
	}
}

// A stream that sends little and stays open (SSE, long polling) ends the
// probe with its status, not at Timeout.
func TestProbeQuietStreamEndsAtStatus(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte(": ok\n\n"))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer srv.Close()

	p := probe.Prober{Client: srv.Client(), Timeout: 3 * time.Second}
	start := time.Now()
	res := p.Probe(context.Background(), []string{srv.URL})
	if d := time.Since(start); d > time.Second {
		t.Errorf("quiet stream took %v, want well under the 3s timeout", d)
	}
	if len(res) != 1 || !res[0].OK {
		t.Fatalf("got %+v", res)
	}
}

type countingTransport struct{ n atomic.Int32 }

func (c *countingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	c.n.Add(1)
	return nil, r.Context().Err()
}

// Once ctx is done no probe starts; each URL fails with ctx's error.
func TestProbeStopsOnCancel(t *testing.T) {
	rt := &countingTransport{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	urls := make([]string, 20)
	for i := range urls {
		urls[i] = "https://h" + strconv.Itoa(i) + ".example/"
	}
	res := probe.Prober{Client: &http.Client{Transport: rt}}.Probe(ctx, urls)
	if n := rt.n.Load(); n != 0 {
		t.Errorf("%d requests after cancel", n)
	}
	for i, r := range res {
		if r.URL != urls[i] || r.OK || !errors.Is(r.Cause, context.Canceled) {
			t.Errorf("result %d: %+v", i, r)
		}
	}
}

// slowLookup answers every host with no address after delay, counting the
// questions.
type slowLookup struct {
	delay time.Duration
	asked atomic.Int32
}

func (l *slowLookup) lookup(context.Context, string) ([]netip.Addr, error) {
	l.asked.Add(1)
	time.Sleep(l.delay)
	return nil, nil
}

func failedHosts(n int) []probe.Result {
	rs := make([]probe.Result, n)
	for i := range rs {
		h := "h" + strconv.Itoa(i) + ".example"
		rs[i] = probe.Result{URL: "https://" + h + "/", Cause: notFound(h)}
	}
	return rs
}

func TestMarkNoAddressParallel(t *testing.T) {
	sys, doh := &slowLookup{delay: 100 * time.Millisecond}, &slowLookup{delay: 100 * time.Millisecond}
	rs := failedHosts(8)
	start := time.Now()
	probe.MarkNoAddress(context.Background(), rs, sys.lookup, doh.lookup)
	if d := time.Since(start); d > 800*time.Millisecond {
		t.Errorf("8 hosts took %v, want about one sys and one doh lookup (200ms)", d)
	}
	for _, r := range rs {
		if !r.NoAddress {
			t.Errorf("%s: NoAddress false", r.URL)
		}
	}
}

func TestMarkNoAddressStopsOnCancel(t *testing.T) {
	sys, doh := &slowLookup{}, &slowLookup{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rs := failedHosts(4)
	probe.MarkNoAddress(ctx, rs, sys.lookup, doh.lookup)
	if n := sys.asked.Load() + doh.asked.Load(); n != 0 {
		t.Errorf("%d lookups after cancel", n)
	}
	for _, r := range rs {
		if r.NoAddress {
			t.Errorf("%s: NoAddress without a lookup", r.URL)
		}
	}
}

func domains(n int) []string {
	ds := make([]string, n)
	for i := range ds {
		ds[i] = "d" + strconv.Itoa(i) + ".example"
	}
	return ds
}

func TestCheckDNSParallel(t *testing.T) {
	sys, doh := &slowLookup{delay: 100 * time.Millisecond}, &slowLookup{delay: 100 * time.Millisecond}
	ds := domains(8)
	start := time.Now()
	rs := probe.CheckDNS(context.Background(), ds, sys.lookup, doh.lookup)
	if d := time.Since(start); d > 800*time.Millisecond {
		t.Errorf("8 domains took %v, want about one sys and one doh lookup (200ms)", d)
	}
	for i, r := range rs {
		if r.Domain != ds[i] || r.Err != nil {
			t.Errorf("result %d: %+v", i, r)
		}
	}
}

func TestCheckDNSStopsOnCancel(t *testing.T) {
	sys, doh := &slowLookup{}, &slowLookup{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ds := domains(4)
	rs := probe.CheckDNS(ctx, ds, sys.lookup, doh.lookup)
	if n := sys.asked.Load() + doh.asked.Load(); n != 0 {
		t.Errorf("%d lookups after cancel", n)
	}
	for i, r := range rs {
		if r.Domain != ds[i] || !errors.Is(r.Err, context.Canceled) {
			t.Errorf("result %d: %+v, want %s failed with context.Canceled", i, r, ds[i])
		}
	}
}
