// Package supervisor runs nfqws2 under our nft table: lock,
// render files, load the table, keep the child alive, and clean up on exit.
package supervisor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/KadirBerkpolat1/SplitDPI/internal/execx"
	"github.com/KadirBerkpolat1/SplitDPI/internal/i18n"
	"github.com/KadirBerkpolat1/SplitDPI/internal/nft"
	"github.com/KadirBerkpolat1/SplitDPI/internal/paths"
	"github.com/KadirBerkpolat1/SplitDPI/internal/render"
)

type Child interface {
	Wait() error
	Signal(os.Signal) error
	StderrTail() []string
	Pid() int
}

type Supervisor struct {
	P     paths.Paths
	Exec  execx.Runner
	Spawn func(argv []string) (Child, error)
	Now   func() time.Time
	Sleep func(time.Duration)
	Log   io.Writer
	// UID and GID own AutoHostlist: nfqws2 appends to it after dropping to --user.
	UID, GID int
	// StartDNS, if set, starts the DNS forwarder before the table (and its
	// redirect) loads. Its ctx ends when Run returns, after the table is gone.
	StartDNS func(ctx context.Context) error
}

type ErrAlreadyRunning struct{ Mode string }

func (e ErrAlreadyRunning) Error() string { return fmt.Sprintf("already running (%s)", e.Mode) }

var ErrCrashLoop = errors.New("nfqws2 keeps crashing")

const (
	maxFails    = 5
	failWindow  = 2 * time.Minute
	maxBackoff  = 30 * time.Second
	stopTimeout = 5 * time.Second
)

// Run holds the lock until it returns. It returns nil after a clean stop
// (ctx done), ErrCrashLoop after 5 child failures within 2 minutes, or the
// setup error. Our table and run files are removed in every case.
func (s *Supervisor) Run(ctx context.Context, out render.Output, ruleset string, meta Status) error {
	log := s.Log
	if log == nil {
		log = io.Discard
	}
	if err := os.MkdirAll(s.P.RunDir, 0o755); err != nil {
		return err
	}
	lf, err := lock(s.P, meta.Mode)
	if err != nil {
		return err
	}
	defer lf.Close()

	st := meta
	st.PID = os.Getpid()
	st.Since = s.Now()
	if err := s.prepare(out); err != nil {
		return s.finish(ctx, st, "failed", err)
	}
	if s.StartDNS != nil {
		dctx, stopDNS := context.WithCancel(ctx)
		defer stopDNS() // runs after finish has deleted the table
		if err := s.StartDNS(dctx); err != nil {
			return s.finish(ctx, st, "failed", fmt.Errorf("DNS forwarder: %w", err))
		}
	}
	if err := nft.Apply(ctx, s.Exec, s.P.NftTable, ruleset); err != nil {
		return s.finish(ctx, st, "failed", err)
	}

	var fails []time.Time
	quick := 0 // consecutive short-lived children, drives the backoff
	for {
		start := s.Now()
		child, err := s.Spawn(out.Argv)
		var tail []string
		if err == nil {
			st.State, st.ChildPID, st.Since = "running", child.Pid(), start
			if werr := writeStatus(s.P, st); werr != nil {
				fmt.Fprintln(log, werr)
			}
			fmt.Fprintln(log, i18n.T("supervisor.started", child.Pid()))
			done := make(chan error, 1)
			go func() { done <- child.Wait() }()
			select {
			case <-ctx.Done():
				stopChild(child, done)
				return s.finish(ctx, st, "stopped", nil)
			case err = <-done:
			}
			tail = child.StderrTail()
			if err == nil {
				err = errors.New("nfqws2 exited unexpectedly")
			}
		}
		now := s.Now()
		fails = append(fails, now)
		for len(fails) > 0 && now.Sub(fails[0]) > failWindow {
			fails = fails[1:]
		}
		if now.Sub(start) >= maxBackoff {
			quick = 0
		}
		st.ChildPID, st.LastError, st.StderrTail = 0, err.Error(), tail
		if len(fails) >= maxFails {
			fmt.Fprintln(log, i18n.T("supervisor.crash_loop", maxFails, failWindow))
			return s.finish(ctx, st, "failed", ErrCrashLoop)
		}
		d := min(time.Second<<quick, maxBackoff)
		quick++
		fmt.Fprintln(log, i18n.T("supervisor.restarting", err, d))
		slept := make(chan struct{})
		go func() { s.Sleep(d); close(slept) }()
		select {
		case <-ctx.Done():
			return s.finish(ctx, st, "stopped", nil)
		case <-slept:
		}
	}
}

// prepare writes the merged lists and makes sure nfqws2's user can append to the autohostlist.
func (s *Supervisor) prepare(out render.Output) error {
	for _, f := range []struct {
		path  string
		lines []string
	}{{out.HostlistFile, out.Hostlist}, {out.ExcludeFile, out.Exclude}} {
		var b bytes.Buffer
		for _, l := range f.lines {
			b.WriteString(l + "\n")
		}
		if err := writeFileAtomic(f.path, b.Bytes()); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(filepath.Dir(s.P.AutoHostlist), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(s.P.AutoHostlist, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Chown(s.P.AutoHostlist, s.UID, s.GID)
}

// finish removes our table and run files (status.json stays), records the
// final state and returns cause, or the cleanup error after a clean stop.
func (s *Supervisor) finish(ctx context.Context, st Status, state string, cause error) error {
	// ctx is usually cancelled here; cleanup must still run.
	cerr := nft.Delete(context.WithoutCancel(ctx), s.Exec, s.P.NftTable)
	if ents, err := os.ReadDir(s.P.RunDir); err == nil {
		for _, e := range ents {
			if e.Name() != filepath.Base(s.P.StatusFile) {
				os.RemoveAll(filepath.Join(s.P.RunDir, e.Name()))
			}
		}
	}
	st.State, st.ChildPID = state, 0
	switch {
	case errors.Is(cause, ErrCrashLoop) && st.LastError != "":
		st.LastError = cause.Error() + ": " + st.LastError
	case cause != nil:
		st.LastError = cause.Error()
	}
	if err := writeStatus(s.P, st); err != nil && cerr == nil {
		cerr = err
	}
	if cause != nil {
		return cause
	}
	return cerr
}

// stopChild sends SIGTERM, then SIGKILL after stopTimeout, and waits for exit.
func stopChild(c Child, done <-chan error) {
	c.Signal(syscall.SIGTERM)
	select {
	case <-done:
	case <-time.After(stopTimeout):
		c.Signal(syscall.SIGKILL)
		<-done
	}
}

// ExecSpawn starts real processes. Output goes to log; the last stderr lines
// are kept for status.json. With guard set (the splitdpi binary), argv runs
// under Guard with guardOpts, which gets SIGTERM if we die and takes argv
// down with it. The child gets its own process group, so a terminal Ctrl-C
// reaches only us and the stop stays orderly.
func ExecSpawn(log io.Writer, guard string, guardOpts ...string) func(argv []string) (Child, error) {
	if log == nil {
		log = io.Discard
	}
	return func(argv []string) (Child, error) {
		if guard != "" {
			argv = slices.Concat([]string{guard, GuardArg}, guardOpts, []string{"--"}, argv)
		}
		cmd := exec.Command(argv[0], argv[1:]...)
		t := &tail{max: 20}
		cmd.Stdout = log
		cmd.Stderr = io.MultiWriter(log, t)
		cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGTERM, Setpgid: true}
		if err := cmd.Start(); err != nil {
			return nil, err
		}
		return &execChild{cmd: cmd, tail: t}, nil
	}
}

type execChild struct {
	cmd  *exec.Cmd
	tail *tail
}

func (c *execChild) Wait() error              { return c.cmd.Wait() }
func (c *execChild) Signal(s os.Signal) error { return c.cmd.Process.Signal(s) }
func (c *execChild) Pid() int                 { return c.cmd.Process.Pid }
func (c *execChild) StderrTail() []string     { return c.tail.lines() }

// tail keeps the last max complete lines written to it.
type tail struct {
	mu      sync.Mutex
	max     int
	partial []byte
	buf     []string
}

func (t *tail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.partial = append(t.partial, p...)
	for {
		i := bytes.IndexByte(t.partial, '\n')
		if i < 0 {
			break
		}
		t.buf = append(t.buf, strings.TrimRight(string(t.partial[:i]), "\r"))
		t.partial = t.partial[i+1:]
		if len(t.buf) > t.max {
			t.buf = t.buf[1:]
		}
	}
	return len(p), nil
}

func (t *tail) lines() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := append([]string{}, t.buf...)
	if len(t.partial) > 0 {
		out = append(out, string(t.partial))
	}
	return out
}
