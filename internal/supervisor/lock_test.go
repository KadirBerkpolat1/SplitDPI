package supervisor

import (
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/KadirBerkpolat1/SplitDPI/internal/paths"
)

// LockInfo holds the flock shared for a moment. A run starting meanwhile,
// like the service while enable waits for it, must not take that for a run
// already there: the service exits 4 then, and nothing restarts it.
func TestLockWaitsOutLockInfo(t *testing.T) {
	p := paths.Under(t.TempDir())
	if err := os.MkdirAll(p.RunDir, 0o755); err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			for {
				select {
				case <-stop:
					return
				default:
					LockInfo(p)
				}
			}
		})
	}
	defer func() { close(stop); wg.Wait() }()
	for i := range 2000 {
		f, err := lock(p, "service")
		if err != nil {
			t.Fatalf("attempt %d with no run holding the lock: %v", i, err)
		}
		f.Close()
	}
}

// A live run is reported at once, not after lockWait.
func TestLockReportsLiveRun(t *testing.T) {
	p := paths.Under(t.TempDir())
	if err := os.MkdirAll(p.RunDir, 0o755); err != nil {
		t.Fatal(err)
	}
	held, err := lock(p, "background")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	start := time.Now()
	_, err = lock(p, "service")
	var already ErrAlreadyRunning
	if !errors.As(err, &already) || already.Mode != "background" {
		t.Fatalf("got %v, want already running (background)", err)
	}
	if d := time.Since(start); d >= lockWait/2 {
		t.Errorf("reported after %s", d)
	}
}
