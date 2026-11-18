package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"golang.org/x/term"

	"github.com/KadirBerkpolat1/SplitDPI/internal/i18n"
)

// Some commands ask a terminal user before a change, such as turning on
// encrypted DNS or installing packages; these are the questions' input.
var (
	promptIn  io.Reader = os.Stdin
	promptTTY           = func() bool { return term.IsTerminal(int(os.Stdin.Fd())) }
)

// askYes writes question and reports whether the answer is one of
// prompt.yes. A cancelled ctx (Ctrl+C, SIGTERM, the terminal closing)
// answers no at once, with ctx's error: the read cannot be interrupted, so
// it is left to the process's exit.
func askYes(ctx context.Context, w io.Writer, question string) (bool, error) {
	fmt.Fprint(w, question+" ")
	in, answer := promptIn, make(chan string, 1)
	go func() {
		line, _ := bufio.NewReader(in).ReadString('\n')
		answer <- line
	}()
	select {
	case <-ctx.Done():
		fmt.Fprintln(w)
		return false, ctx.Err()
	case line := <-answer:
		return slices.Contains(strings.Split(i18n.T("prompt.yes"), ","), strings.ToLower(strings.TrimSpace(line))), nil
	}
}
