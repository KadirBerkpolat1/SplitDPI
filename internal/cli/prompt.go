package cli

import (
	"bufio"
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

// askYes writes question and reports whether the answer is one of prompt.yes.
func askYes(w io.Writer, question string) bool {
	fmt.Fprint(w, question+" ")
	line, _ := bufio.NewReader(promptIn).ReadString('\n')
	return slices.Contains(strings.Split(i18n.T("prompt.yes"), ","), strings.ToLower(strings.TrimSpace(line)))
}
