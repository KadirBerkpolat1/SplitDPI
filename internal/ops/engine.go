package ops

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/KadirBerkpolat1/SplitDPI/internal/engine"
)

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
		r = resp.Body
	}
	return engine.Install(o.P, r, engine.SHA256, o.GOARCH)
}

func (o *Ops) EngineStatus() (engine.Engine, bool) {
	return engine.Installed(o.P)
}
