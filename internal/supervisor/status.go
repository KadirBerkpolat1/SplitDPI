package supervisor

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/KadirBerkpolat1/SplitDPI/internal/paths"
)

// Status is written to StatusFile on every state change.
type Status struct {
	State      string    `json:"state"`                // running|restarting|stopped|failed
	Mode       string    `json:"mode"`                 // foreground|background|service|scan
	PID        int       `json:"pid"`                  // the supervisor
	ChildPID   int       `json:"child_pid,omitempty"`  // the guard, nfqws2's parent
	Nfqws2PID  int       `json:"nfqws2_pid,omitempty"` // nfqws2 itself, once the guard reports it
	Strategy   string    `json:"strategy"`
	Engine     string    `json:"engine"`
	Apps       []string  `json:"apps"`
	LastError  string    `json:"last_error,omitempty"`
	StderrTail []string  `json:"stderr_tail,omitempty"`
	Since      time.Time `json:"since"`
}

// StateRestarting is the state between nfqws2 exiting and its restart: the
// supervisor still holds the lock, but no nfqws2 handles the queue.
const StateRestarting = "restarting"

// On reports whether the supervisor that wrote st was on: running, or
// restarting nfqws2. Whether it still is, only the lock tells.
func (st Status) On() bool { return st.State == "running" || st.State == StateRestarting }

func ReadStatus(p paths.Paths) (Status, error) {
	var st Status
	b, err := os.ReadFile(p.StatusFile)
	if err != nil {
		return st, err
	}
	if err := json.Unmarshal(b, &st); err != nil {
		return st, fmt.Errorf("%s: %w", p.StatusFile, err)
	}
	return st, nil
}

func writeStatus(p paths.Paths, st Status) error {
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(p.StatusFile, append(b, '\n'))
}

func writeFileAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

type lockInfo struct {
	PID  int    `json:"pid"`
	Mode string `json:"mode"`
}

// LockInfo reports the PID and mode recorded in LockFile and whether a live
// process still holds the flock. A stale file (kill -9, power loss) is not held.
func LockInfo(p paths.Paths) (pid int, mode string, held bool) {
	f, err := os.Open(p.LockFile)
	if err != nil {
		return 0, "", false
	}
	defer f.Close()
	var li lockInfo
	_ = json.NewDecoder(f).Decode(&li) // a torn or empty file just reads as zero values
	err = syscall.Flock(int(f.Fd()), syscall.LOCK_SH|syscall.LOCK_NB)
	if err == nil {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	}
	return li.PID, li.Mode, errors.Is(err, syscall.EWOULDBLOCK)
}

// lockWait bounds how long lock waits out LockInfo calls: each holds the
// flock shared for a moment, and an exclusive one cannot be taken meanwhile.
const lockWait = 2 * time.Second

// Lock takes the exclusive run lock and records our PID and mode. The
// returned file must stay open for as long as the lock is needed.
// ErrAlreadyRunning means that a live run holds it; a flock held only shared,
// by LockInfo in status, the TUI or enable, is waited out.
func Lock(p paths.Paths, mode string) (*os.File, error) {
	if err := os.MkdirAll(p.RunDir, 0o755); err != nil {
		return nil, err
	}
	deadline := time.Now().Add(lockWait)
	for {
		f, err := flockPath(p, deadline)
		if err != nil {
			return nil, err
		}
		// A file unlinked after we opened it, as revert clears RunDir, is
		// locked for no run that opens the path: lock the one there now.
		if !atPath(f, p.LockFile) {
			f.Close()
			continue
		}
		b, _ := json.Marshal(lockInfo{PID: os.Getpid(), Mode: mode})
		if err := f.Truncate(0); err != nil {
			f.Close()
			return nil, err
		}
		if _, err := f.WriteAt(b, 0); err != nil {
			f.Close()
			return nil, err
		}
		return f, nil
	}
}

// flockPath opens LockFile and takes its exclusive flock, waiting out shared
// ones until deadline.
func flockPath(p paths.Paths, deadline time.Time) (*os.File, error) {
	f, err := os.OpenFile(p.LockFile, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, err
	}
	for delay := time.Millisecond; ; delay = min(2*delay, 50*time.Millisecond) {
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			break
		}
		// Only a run holds it exclusively, and then LockInfo cannot take it shared.
		if _, running, held := LockInfo(p); held {
			f.Close()
			return nil, ErrAlreadyRunning{Mode: running}
		}
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(delay)
	}
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("%s: %w", p.LockFile, err)
	}
	return f, nil
}

// atPath reports whether f is the file at path.
func atPath(f *os.File, path string) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	cur, err := os.Stat(path)
	return err == nil && os.SameFile(fi, cur)
}
