package supervisor

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/KadirBerkpolat1/SplitDPI/internal/execx"
	"github.com/KadirBerkpolat1/SplitDPI/internal/nft"
)

// GuardArg, as the first argument, makes the splitdpi binary run Guard.
const GuardArg = "__guard"

const (
	guardKillAfter = 3 * time.Second
	dropChainFlag  = "--drop-chain="
)

// DropChainOpt makes the guard delete chain of our inet table when it is told
// to stop, which includes the supervisor dying.
func DropChainOpt(table, chain string) string { return dropChainFlag + table + "/" + chain }

// Guard runs `[options] -- argv` and makes sure argv dies with the
// supervisor. nfqws2 cannot carry Pdeathsig itself: the kernel clears it when
// nfqws2 drops to its --user (prctl(2)). The guard stays root, so its own
// Pdeathsig holds. On the first SIGTERM or SIGINT it drops the chains named
// by DropChainOpt, then forwards SIGTERM and kills the child after 3 s.
// It returns the child's exit code.
func Guard(args []string) int {
	var drop [][2]string // table, chain
	for len(args) > 0 && args[0] != "--" {
		table, chain, ok := strings.Cut(strings.TrimPrefix(args[0], dropChainFlag), "/")
		if !strings.HasPrefix(args[0], dropChainFlag) || !ok {
			os.Stderr.WriteString("guard: unknown option " + args[0] + "\n")
			return 2
		}
		drop = append(drop, [2]string{table, chain})
		args = args[1:]
	}
	if len(args) < 2 {
		return 2
	}
	argv := args[1:]

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGTERM, syscall.SIGINT)
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		os.Stderr.WriteString(err.Error() + "\n")
		return 1
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	var kill <-chan time.Time
	for {
		select {
		case <-sigs:
			if kill == nil {
				for _, d := range drop {
					if err := nft.DeleteChain(context.Background(), execx.Real, d[0], d[1]); err != nil {
						os.Stderr.WriteString("guard: " + err.Error() + "\n")
					}
				}
				kill = time.After(guardKillAfter)
			}
			cmd.Process.Signal(syscall.SIGTERM)
		case <-kill:
			cmd.Process.Kill()
		case err := <-done:
			var exit *exec.ExitError
			switch {
			case err == nil:
				return 0
			case errors.As(err, &exit) && exit.ExitCode() >= 0:
				return exit.ExitCode()
			}
			return 1
		}
	}
}
