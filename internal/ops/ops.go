// Package ops holds the operations shared by the CLI and the TUI.
// Operations return data and never print.
package ops

import (
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"time"

	"github.com/KadirBerkpolat1/SplitDPI/internal/execx"
	"github.com/KadirBerkpolat1/SplitDPI/internal/paths"
	"github.com/KadirBerkpolat1/SplitDPI/internal/supervisor"
)

// Network limits, so that a server which stops answering cannot hang a
// command that runs without a deadline. Variables so tests can shorten them.
var (
	headerTimeout     = 30 * time.Second // wait for the response headers of any request
	lookupTimeout     = 15 * time.Second // the whole ISP lookup
	presetsTimeout    = 30 * time.Second // the whole preset update
	engineIdleTimeout = 30 * time.Second // engine download without new data; no overall limit
)

type Ops struct {
	P      paths.Paths
	Exec   execx.Runner
	Root   fs.FS
	HTTP   *http.Client
	Now    func() time.Time
	Euid   func() int
	GOARCH string
	// LookPath finds a command on PATH; nil means exec.LookPath.
	LookPath func(string) (string, error)
	// Spawn starts nfqws2; nil means supervisor.ExecSpawn under this binary as guard.
	Spawn func(argv []string) (supervisor.Child, error)
	// Log receives nfqws2 output and supervisor messages; nil discards them.
	Log io.Writer
	// Detach starts the process of `run --background` and reports when it
	// exits; nil means c.Start.
	Detach func(c *exec.Cmd) (exited <-chan error, err error)
}

// New returns Ops wired to the real system. Our own files live under p;
// Root is always the host "/" so detection sees the real /proc, /sys and /etc.
func New(p paths.Paths) *Ops {
	// No Client.Timeout: it would cut the large engine download.
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.ResponseHeaderTimeout = headerTimeout
	return &Ops{
		P:      p,
		Exec:   execx.Real,
		Root:   os.DirFS("/"),
		HTTP:   &http.Client{Transport: tr},
		Now:    time.Now,
		Euid:   os.Geteuid,
		GOARCH: runtime.GOARCH,
		Log:    os.Stderr,
	}
}
