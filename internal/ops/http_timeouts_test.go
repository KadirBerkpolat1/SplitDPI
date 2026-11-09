package ops

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/KadirBerkpolat1/SplitDPI/internal/engine"
	"github.com/KadirBerkpolat1/SplitDPI/internal/paths"
)

// slack is how late a limit may fire on a loaded test machine.
const slack = 2 * time.Second

// netOps are the operations that reach the network through Ops.HTTP, with the
// limit that ends each once the response headers arrived and its error.
var netOps = []struct {
	name    string
	limit   *time.Duration
	stalled error
	run     func(context.Context, *Ops) error
}{
	{"engine download", &engineIdleTimeout, errStalled, func(ctx context.Context, o *Ops) error {
		_, err := o.EngineInstall(ctx, "")
		return err
	}},
	{"presets update", &presetsTimeout, context.DeadlineExceeded, func(ctx context.Context, o *Ops) error {
		_, _, err := o.PresetsUpdate(ctx)
		return err
	}},
	{"ISP lookup", &lookupTimeout, context.DeadlineExceeded, func(ctx context.Context, o *Ops) error {
		_, _, err := o.Detect(ctx, 0, "")
		return err
	}},
}

// shorten sets *limit to d until the test ends.
func shorten(t *testing.T, limit *time.Duration, d time.Duration) {
	old := *limit
	*limit = d
	t.Cleanup(func() { *limit = old })
}

// stallServer holds every request open until the client gives up or the test
// ends. With headers it first sends them and the start of a gzip stream.
func stallServer(t *testing.T, headers bool) *httptest.Server {
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	zw.Flush() // a header and an empty block: a reader waits for more
	done := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if headers {
			w.Write(gz.Bytes())
			w.(http.Flusher).Flush()
		}
		select {
		case <-r.Context().Done():
		case <-done:
		}
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(done) }) // runs first, so Close waits on no handler
	return srv
}

// realOps is New's Ops with every request sent to srv over its own transport.
func realOps(t *testing.T, srv *httptest.Server) *Ops {
	o := New(paths.Under(t.TempDir()))
	o.GOARCH = "amd64" // the engine installer reads the download only for a known arch
	target, _ := url.Parse(srv.URL)
	tr := o.HTTP.Transport
	o.HTTP.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		r = r.Clone(r.Context())
		r.URL.Scheme, r.URL.Host = target.Scheme, target.Host
		return tr.RoundTrip(r)
	})
	return o
}

// runWithin runs op on o and checks that it returns within limit. A later
// deadline ends it, so a broken limit fails the test instead of hanging it.
func runWithin(t *testing.T, limit time.Duration, o *Ops, op func(context.Context, *Ops) error) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), limit+2*slack)
	defer cancel()
	start := time.Now()
	err := op(ctx, o)
	if took := time.Since(start); took > limit+slack {
		t.Errorf("took %s, limit %s", took, limit)
	}
	return err
}

func TestNetOpsFailWithoutResponseHeaders(t *testing.T) {
	const limit = 200 * time.Millisecond
	shorten(t, &headerTimeout, limit)
	srv := stallServer(t, false)
	for _, op := range netOps {
		t.Run(op.name, func(t *testing.T) {
			err := runWithin(t, limit, realOps(t, srv), op.run)
			if err == nil || !strings.Contains(err.Error(), "timeout awaiting response headers") {
				t.Errorf("err %v", err)
			}
		})
	}
}

func TestNetOpsFailWhenBodyStalls(t *testing.T) {
	const limit = 250 * time.Millisecond
	srv := stallServer(t, true)
	for _, op := range netOps {
		t.Run(op.name, func(t *testing.T) {
			shorten(t, op.limit, limit)
			if err := runWithin(t, limit, realOps(t, srv), op.run); !errors.Is(err, op.stalled) {
				t.Errorf("err %v, want %v", err, op.stalled)
			}
		})
	}
}

func TestEngineDownloadOutlastsIdleLimit(t *testing.T) {
	const idle = 500 * time.Millisecond
	shorten(t, &engineIdleTimeout, idle)
	piece := bytes.Repeat([]byte{'x'}, 1024)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 60 pieces 25 ms apart: 1.5 s in all, never a gap near the idle limit.
		for range 60 {
			w.Write(piece)
			w.(http.Flusher).Flush()
			select {
			case <-r.Context().Done():
				return
			case <-time.After(25 * time.Millisecond):
			}
		}
	}))
	defer srv.Close()
	o := realOps(t, srv)
	start := time.Now()
	_, err := o.EngineInstall(context.Background(), "")
	// Only a fully read body reaches the checksum, which the fake data fails.
	if !errors.Is(err, engine.ErrChecksum) {
		t.Fatalf("err %v", err)
	}
	if took := time.Since(start); took < 2*idle {
		t.Errorf("took %s, want over %s", took, 2*idle)
	}
}
