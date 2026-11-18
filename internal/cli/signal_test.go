package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/KadirBerkpolat1/SplitDPI/internal/ops"
)

// A-1, A-2, C-2: SIGTERM, SIGINT and SIGHUP (the terminal closing) cancel
// the command's context instead of killing the process, so that the command
// cleans up; signals during that cleanup are caught too.
func TestMainSignalCancelsCommand(t *testing.T) {
	for _, first := range []syscall.Signal{syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP} {
		t.Run(first.String(), func(t *testing.T) {
			cleaned := false
			commands["signaltest"] = Command{Name: "signaltest", Run: func(ctx context.Context, _ *ops.Ops, _ io.Writer, _ []string) error {
				if err := syscall.Kill(os.Getpid(), first); err != nil {
					return err
				}
				select {
				case <-ctx.Done():
				case <-time.After(5 * time.Second):
					return errors.New("the signal did not cancel the context")
				}
				for _, sig := range []syscall.Signal{syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM} {
					if err := syscall.Kill(os.Getpid(), sig); err != nil {
						return err
					}
				}
				time.Sleep(200 * time.Millisecond) // the signals arrive while this cleans up
				cleaned = true
				return nil
			}}
			t.Cleanup(func() { delete(commands, "signaltest") })
			out, code := run(t, 1000, "signaltest")
			eq(t, code, 0)
			eq(t, out, "")
			if !cleaned {
				t.Error("the command's cleanup did not finish")
			}
		})
	}
}

// blockingInput is a terminal no one types into: Read tells started, then
// blocks until the test ends.
type blockingInput struct {
	started chan struct{}
	done    chan struct{}
}

func (b blockingInput) Read([]byte) (int, error) {
	close(b.started)
	<-b.done
	return 0, io.EOF
}

// Ctrl+C at a question answers no at once and ends the command with the
// cancel, without waiting for Enter.
func TestPromptCtrlCEndsCommand(t *testing.T) {
	in := blockingInput{make(chan struct{}), make(chan struct{})}
	t.Cleanup(func() { close(in.done) })
	oldIn, oldTTY := promptIn, promptTTY
	t.Cleanup(func() { promptIn, promptTTY = oldIn, oldTTY })
	promptIn, promptTTY = in, func() bool { return true }
	o, f, _ := missingOps(t, debianRoot(t, nil), "curl")
	go func() {
		<-in.started
		_ = syscall.Kill(os.Getpid(), syscall.SIGINT)
	}()
	type result struct {
		out  string
		code int
	}
	done := make(chan result, 1)
	go func() {
		out, code := runOps(t, o, "deps", "install")
		done <- result{out, code}
	}()
	select {
	case r := <-done:
		eq(t, r.code, 1)
		has(t, r.out, "Install them now? [y/N] \n")
		has(t, r.out, context.Canceled.Error())
		notHas(t, r.out, "Installing")
		if f.saw("apt-get install -y curl") {
			t.Errorf("installed after Ctrl+C: %q", f.calls)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the question still waits for Enter after Ctrl+C")
	}
}

func TestAskYesCancelled(t *testing.T) {
	in := blockingInput{make(chan struct{}), make(chan struct{})}
	t.Cleanup(func() { close(in.done) })
	oldIn := promptIn
	t.Cleanup(func() { promptIn = oldIn })
	promptIn = in
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-in.started
		cancel()
	}()
	yes, err := askYes(ctx, io.Discard, "Sure?")
	if yes || !errors.Is(err, context.Canceled) {
		t.Errorf("askYes = %v, %v; want false, context.Canceled", yes, err)
	}
}
