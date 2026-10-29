// Package ops holds the operations shared by the CLI and the TUI.
// Operations return data and never print.
package ops

import (
	"io"
	"io/fs"
	"net/http"
	"os"
	"runtime"
	"time"

	"github.com/KadirBerkpolat1/SplitDPI/internal/execx"
	"github.com/KadirBerkpolat1/SplitDPI/internal/paths"
	"github.com/KadirBerkpolat1/SplitDPI/internal/supervisor"
)

type Ops struct {
	P      paths.Paths
	Exec   execx.Runner
	Root   fs.FS
	HTTP   *http.Client
	Now    func() time.Time
	Euid   func() int
	GOARCH string
	// Spawn starts nfqws2; nil means supervisor.ExecSpawn under this binary as guard.
	Spawn func(argv []string) (supervisor.Child, error)
	// Log receives nfqws2 output and supervisor messages; nil discards them.
	Log io.Writer
}

// New returns Ops wired to the real system. Our own files live under p;
// Root is always the host "/" so detection sees the real /proc, /sys and /etc.
func New(p paths.Paths) *Ops {
	return &Ops{
		P:      p,
		Exec:   execx.Real,
		Root:   os.DirFS("/"),
		HTTP:   &http.Client{},
		Now:    time.Now,
		Euid:   os.Geteuid,
		GOARCH: runtime.GOARCH,
		Log:    os.Stderr,
	}
}
