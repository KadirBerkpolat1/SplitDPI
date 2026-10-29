// Package cli parses arguments and prints results of ops.
package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"

	"golang.org/x/term"

	"github.com/KadirBerkpolat1/SplitDPI/internal/config"
	"github.com/KadirBerkpolat1/SplitDPI/internal/i18n"
	"github.com/KadirBerkpolat1/SplitDPI/internal/ops"
	"github.com/KadirBerkpolat1/SplitDPI/internal/tui"
)

// Version is set at build time with -ldflags "-X .../internal/cli.Version=...".
var Version = "dev"

type Command struct {
	Name string
	Root bool
	Help string // i18n key
	Run  func(ctx context.Context, o *ops.Ops, w io.Writer, args []string) error
}

// usageError makes Main print the message and exit 2.
type usageError string

func (e usageError) Error() string { return string(e) }

// exitError makes Main print err and exit with code.
type exitError struct {
	code int
	err  error
}

func (e exitError) Error() string { return e.err.Error() }

var commands = map[string]Command{
	"version": {Name: "version", Help: "cmd.version.help", Run: runVersion},
}

// Main runs one command and returns the process exit code:
// 0 ok, 1 failure, 2 usage, 3 crash loop.
func Main(args []string, stdout, stderr io.Writer, o *ops.Ops) int {
	lang, rest, ok := splitLang(args)
	if lang == "" {
		if c, err := config.Load(o.P); err == nil {
			lang = c.Lang
		}
	}
	i18n.Init(i18n.Detect(lang, os.Getenv))
	if !ok {
		fmt.Fprintln(stderr, i18n.T("err.lang_value"))
		return 2
	}
	if len(rest) == 0 {
		return openTUI(args, stdout, stderr, o)
	}
	if rest[0] == "--help" {
		usage(stdout)
		return 0
	}
	c, found := commands[rest[0]]
	if !found {
		fmt.Fprintln(stderr, i18n.T("err.unknown_command", rest[0]))
		usage(stderr)
		return 2
	}
	if c.Root && o.Euid() != 0 {
		fmt.Fprintln(stderr, i18n.T("err.need_root", "sudo splitdpi "+strings.Join(args, " ")))
		return 1
	}
	if err := c.Run(context.Background(), o, stdout, rest[1:]); err != nil {
		fmt.Fprintln(stderr, err)
		if _, ok := err.(usageError); ok {
			return 2
		}
		if e, ok := err.(exitError); ok {
			return e.code
		}
		return 1
	}
	return 0
}

// openTUI is `splitdpi` with no command: the TUI, as root like every other
// command. Without a terminal there is nothing to draw on, so it prints usage.
func openTUI(args []string, stdout, stderr io.Writer, o *ops.Ops) int {
	if o.Euid() != 0 {
		fmt.Fprintln(stderr, i18n.T("err.need_root", strings.TrimSpace("sudo splitdpi "+strings.Join(args, " "))))
		return 1
	}
	if f, ok := stdout.(*os.File); !ok || !term.IsTerminal(int(f.Fd())) {
		usage(stderr)
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM)
	defer stop()
	if err := tui.Run(ctx, o, Version); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

// splitLang removes "--lang X" or "--lang=X" from args. ok is false when the value is missing.
func splitLang(args []string) (lang string, rest []string, ok bool) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--lang":
			if i+1 >= len(args) {
				return "", nil, false
			}
			i++
			lang = args[i]
		case strings.HasPrefix(a, "--lang="):
			lang = strings.TrimPrefix(a, "--lang=")
		default:
			rest = append(rest, a)
		}
	}
	return lang, rest, true
}

func usage(w io.Writer) {
	fmt.Fprintln(w, i18n.T("usage.header"))
	fmt.Fprintln(w)
	fmt.Fprintln(w, i18n.T("usage.commands"))
	names := make([]string, 0, len(commands))
	for n := range commands {
		names = append(names, n)
	}
	slices.Sort(names)
	for _, n := range names {
		fmt.Fprintf(w, "  %-10s %s\n", n, i18n.T(commands[n].Help))
	}
}

func runVersion(_ context.Context, _ *ops.Ops, w io.Writer, _ []string) error {
	fmt.Fprintln(w, "splitdpi "+Version)
	return nil
}
