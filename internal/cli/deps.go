package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/KadirBerkpolat1/SplitDPI/internal/i18n"
	"github.com/KadirBerkpolat1/SplitDPI/internal/ops"
	"github.com/KadirBerkpolat1/SplitDPI/internal/sysinfo"
)

func init() {
	commands["deps"] = Command{Name: "deps", Root: true, Help: "cmd.deps.help", Run: runDeps}
}

func runDeps(ctx context.Context, o *ops.Ops, w io.Writer, args []string) error {
	install, yes := false, false
	switch {
	case len(args) == 0:
	case len(args) == 1 && args[0] == "install":
		install = true
	case len(args) == 2 && args[0] == "install" && args[1] == "--yes":
		install, yes = true, true
	default:
		return usageError(i18n.T("deps.usage"))
	}
	p := o.MissingPackages()
	if len(p.Tools) == 0 {
		fmt.Fprintln(w, i18n.T("deps.none"))
		return nil
	}
	printPackages(w, p)
	if !p.CanInstall() {
		unknown := i18n.T("deps.unknown_pm", strings.Join(p.Names, ", "))
		if install {
			return errors.New(unknown)
		}
		fmt.Fprintln(w, unknown)
		return nil
	}
	if !install {
		fmt.Fprintln(w, i18n.T("deps.hint", ops.InstallHint(p)))
		return nil
	}
	if !yes {
		if !promptTTY() {
			fmt.Fprintln(w, i18n.T("deps.confirm", "sudo splitdpi deps install --yes"))
			return nil
		}
		if !askYes(w, i18n.T("deps.ask")) {
			fmt.Fprintln(w, i18n.T("deps.declined"))
			return nil
		}
	}
	names := strings.Join(p.Names, ", ")
	fmt.Fprintln(w, i18n.T("deps.installing", names, string(p.Manager)))
	if err := o.InstallPackages(ctx, p); err != nil {
		return err
	}
	fmt.Fprintln(w, i18n.T("deps.installed", names))
	return nil
}

// printPackages lists the missing packages, why each is needed, and, where
// splitdpi can install them, the command it runs and how to undo it.
func printPackages(w io.Writer, p sysinfo.Packages) {
	fmt.Fprintln(w, i18n.T("deps.missing", string(p.Manager)))
	for i, t := range p.Tools {
		fmt.Fprintln(w, "  "+i18n.T("deps.why."+t.ID, p.Names[i]))
	}
	if !p.CanInstall() {
		return
	}
	fmt.Fprintln(w, i18n.T("deps.runs", p.Command()))
	fmt.Fprintln(w, i18n.T("deps.remove", p.RemoveHint()))
}
