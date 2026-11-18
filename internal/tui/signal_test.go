package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"slices"
	"syscall"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/KadirBerkpolat1/SplitDPI/internal/scan"
)

// eioTerminal is a terminal that is gone: writes fail with EIO, and reads
// too when read is set.
type eioTerminal struct{ read bool }

func (eioTerminal) Write([]byte) (int, error) { return 0, syscall.EIO }

func (t eioTerminal) Read([]byte) (int, error) {
	if t.read {
		return 0, syscall.EIO
	}
	select {} // a terminal no one types into
}

// blockingScan makes f's scan run until it is cancelled; started is closed
// once it runs.
func blockingScan(f *fake) (started chan struct{}) {
	started = make(chan struct{})
	f.scanFn = func(ctx context.Context, _ scan.Options, _ func(string)) (scan.Report, error) {
		close(started)
		<-ctx.Done()
		f.record("scan cleaned up")
		return scan.Report{}, ctx.Err()
	}
	return started
}

// scanningUnder is a setup scan of the service f runs, under ctx, with its
// command running outside a program.
func scanningUnder(t *testing.T, f *fake, ctx context.Context) Model {
	t.Helper()
	runningService(f)
	started := blockingScan(f)
	f.set = trSet()
	f.detected = scan.ASNInfo{ASN: 9121, Country: "TR", Org: "Turk Telekom"}
	f.cfg.Apps = nil
	m := newModel(t, f)
	m.ctx = ctx
	m = press(t, m, "w")
	m, cmd := update(t, m, key("enter"))
	go cmd()
	<-started
	return m
}

// runProgram runs m's program loop and cleanup on a terminal that is gone,
// with in as its input, and fails if it does not return in time.
func runProgram(t *testing.T, m Model, in eioTerminal, end func()) error {
	t.Helper()
	m.out, m.every = eioTerminal{}, tea.Tick
	done := make(chan error, 1)
	go func() { done <- m.run(tea.WithInput(in)) }()
	end()
	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("the program never ended: Run hangs")
		return nil
	}
}

// A-1, A-2, C-2: SIGTERM, SIGINT or SIGHUP, caught as the caller's context
// as cli.Main does, end the program loop every time, and the cleanup still
// waits for the scan and starts the service it stopped again, though every
// write to the gone terminal fails.
func TestSignalEndsProgramAndCleansUp(t *testing.T) {
	for i, sig := range slices.Repeat([]syscall.Signal{syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP}, 8) {
		t.Run(fmt.Sprintf("%d-%v", i, sig), func(t *testing.T) {
			f := newFake()
			ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
			defer stop()
			m := scanningUnder(t, f, ctx)
			before := f.count("detect foreign")
			err := runProgram(t, m, eioTerminal{}, func() {
				for f.count("detect foreign") == before { // Init ran: the loop is up
					time.Sleep(time.Millisecond)
				}
				if err := syscall.Kill(os.Getpid(), sig); err != nil {
					t.Error(err)
				}
			})
			if err != nil {
				t.Errorf("Run after %v: %v, want nil", sig, err)
			}
			if !f.called("scan cleaned up") || !f.called("enable") {
				t.Errorf("Run returned before the scan ended and the service ran again: %q", f.calls)
			}
		})
	}
}

// A-2: a terminal lost without a signal, whose reads fail, ends the program
// too, and the cleanup goes to the end.
func TestLostTerminalCleansUp(t *testing.T) {
	f := newFake()
	m := scanningUnder(t, f, context.Background())
	if err := runProgram(t, m, eioTerminal{read: true}, func() {}); !errors.Is(err, syscall.EIO) {
		t.Errorf("Run: %v, want the read's EIO", err)
	}
	if !f.called("scan cleaned up") || !f.called("enable") {
		t.Errorf("Run returned before the scan ended and the service ran again: %q", f.calls)
	}
}

// A-3: quitting during a scan that stopped no bypass still waits until the
// scan has removed its rules.
func TestQuitDuringScanWaitsWithoutBypass(t *testing.T) {
	f := newFake()
	started := make(chan struct{})
	f.scanFn = func(ctx context.Context, _ scan.Options, _ func(string)) (scan.Report, error) {
		close(started)
		<-ctx.Done()
		time.Sleep(50 * time.Millisecond) // the trial's rules and blockcheck2 go
		f.record("scan cleaned up")
		return scan.Report{}, ctx.Err()
	}
	m, cmd := toScan(t, f)
	go cmd()
	<-started
	_, quit := update(t, m, key("q"))
	if !f.called("scan cleaned up") {
		t.Errorf("q quit before the scan cleaned up: %q", f.calls)
	}
	if quit == nil {
		t.Fatal("q did not quit")
	}
}

// A-1, A-3: a scan whose command the program never started (a program
// ended by its context drops it) is not waited for, whatever it would
// have stopped.
func TestQuitBeforeScanStartedDoesNotHang(t *testing.T) {
	f := newFake()
	runningService(f)
	blockingScan(f)
	m, _ := toScan(t, f)
	done := make(chan struct{})
	go func() { update(t, m, key("q")); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("q waits for a scan that never started")
	}
}
