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
	"github.com/KadirBerkpolat1/SplitDPI/internal/i18n"
	"github.com/KadirBerkpolat1/SplitDPI/internal/supervisor"
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
	return o.installEngine(r, engine.SHA256)
}

// installEngine installs the engine in tarball, then removes the engines that
// current no longer names. A running instance or a scan may still use one of
// those, so while one holds the run lock or the engine lock they stay until
// the next `run` starts.
func (o *Ops) installEngine(tarball io.Reader, wantSHA256 string) (engine.Engine, error) {
	e, err := engine.Install(o.P, tarball, wantSHA256, o.GOARCH)
	if err != nil {
		return e, err
	}
	o.removeOldEngines(o.log(), func() bool {
		_, _, held := supervisor.LockInfo(o.P)
		return held
	})
	return e, nil
}

// removeOldEngines runs engine.RemoveOld. A failure only leaves disk space
// taken, so it is logged, not returned.
func (o *Ops) removeOldEngines(log io.Writer, running func() bool, keep ...engine.Engine) {
	if err := engine.RemoveOld(o.P, running, keep...); err != nil {
		fmt.Fprintln(log, i18n.T("engine.remove_old_failed", err))
	}
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
