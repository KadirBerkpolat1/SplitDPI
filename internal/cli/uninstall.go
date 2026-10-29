package cli

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/KadirBerkpolat1/SplitDPI/internal/i18n"
	"github.com/KadirBerkpolat1/SplitDPI/internal/ops"
)

func init() {
	commands["uninstall"] = Command{Name: "uninstall", Root: true, Help: "cmd.uninstall.help", Run: runUninstall}
}

func runUninstall(ctx context.Context, o *ops.Ops, w io.Writer, args []string) error {
	switch {
	case len(args) == 0:
		printUninstallPlan(w, o.UninstallPlan())
		return nil
	case len(args) == 1 && args[0] == "--yes":
	default:
		return usageError(i18n.T("uninstall.usage"))
	}
	r, err := o.Uninstall(ctx)
	for _, d := range r.Removed {
		fmt.Fprintln(w, i18n.T("uninstall.removed", d))
	}
	if err == nil {
		fmt.Fprintln(w, i18n.T("uninstall.done"))
		return nil
	}
	for _, l := range r.Leftover {
		fmt.Fprintln(w, i18n.T("uninstall.leftover", l))
	}
	if errors.Is(err, ops.ErrUninstallIncomplete) {
		return errors.New(i18n.T("uninstall.not_clean"))
	}
	return err
}

func printUninstallPlan(w io.Writer, p ops.UninstallPlan) {
	if p.Service == "" && len(p.Paths) == 0 {
		fmt.Fprintln(w, i18n.T("uninstall.nothing"))
		return
	}
	fmt.Fprintln(w, i18n.T("uninstall.plan"))
	if p.Service != "" {
		fmt.Fprintln(w, i18n.T("uninstall.item", p.Service))
	}
	for _, f := range p.Paths {
		fmt.Fprintln(w, i18n.T("uninstall.item", f))
	}
	fmt.Fprintln(w, i18n.T("uninstall.confirm"))
}
