package ops

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/KadirBerkpolat1/SplitDPI/internal/engine"
)

// errStalled ends a download that receives no data for engineIdleTimeout.
var errStalled = errors.New("download stalled")

// EngineInstall installs the pinned engine from the file from, or downloads it when from is "".
func (o *Ops) EngineInstall(ctx context.Context, from string) (engine.Engine, error) {
	var r io.Reader
	if from != "" {
		f, err := os.Open(from)
		if err != nil {
			return engine.Engine{}, err
		}
		defer f.Close()
		r = f
	} else {
		ctx, cancel := context.WithCancelCause(ctx)
		defer cancel(nil)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, engine.URL, nil)
		if err != nil {
			return engine.Engine{}, err
		}
		resp, err := o.HTTP.Do(req)
		if err != nil {
			return engine.Engine{}, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return engine.Engine{}, fmt.Errorf("%s: %s", engine.URL, resp.Status)
		}
		r = newStallReader(ctx, cancel, resp.Body, engineIdleTimeout)
	}
	return engine.Install(o.P, r, engine.SHA256, o.GOARCH)
}

func (o *Ops) EngineStatus() (engine.Engine, bool) {
	return engine.Installed(o.P)
}

// stallReader cancels its download when one Read waits longer than limit for
// data, so a slow download fails only once it stops.
type stallReader struct {
	ctx   context.Context
	body  io.Reader
	limit time.Duration
	timer *time.Timer
}

func newStallReader(ctx context.Context, cancel context.CancelCauseFunc, body io.Reader, limit time.Duration) *stallReader {
	t := time.AfterFunc(limit, func() { cancel(errStalled) })
	t.Stop()
	return &stallReader{ctx: ctx, body: body, limit: limit, timer: t}
}

func (s *stallReader) Read(p []byte) (int, error) {
	s.timer.Reset(s.limit)
	n, err := s.body.Read(p)
	s.timer.Stop()
	// HTTP/2 reports the cancel as context.Canceled, not as its cause.
	if err != nil && errors.Is(context.Cause(s.ctx), errStalled) {
		err = fmt.Errorf("%w: no data for %s", errStalled, s.limit)
	}
	return n, err
}
