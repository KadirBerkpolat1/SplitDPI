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
	State      string    `json:"state"`                // running|stopped|failed
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

// lock takes the exclusive flock and records our PID and mode. The returned
// file must stay open for as long as the lock is needed. ErrAlreadyRunning
// means that a live run holds it; a flock held only shared, by LockInfo in
// status, the TUI or enable, is waited out.
func lock(p paths.Paths, mode string) (*os.File, error) {
	f, err := os.OpenFile(p.LockFile, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(lockWait)
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
