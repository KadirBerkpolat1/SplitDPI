package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/KadirBerkpolat1/SplitDPI/internal/i18n"
	"github.com/KadirBerkpolat1/SplitDPI/internal/ops"
)

func init() {
	commands["takeover"] = Command{Name: "takeover", Root: true, Help: "cmd.takeover.help", Run: runTakeover}
}

func runTakeover(ctx context.Context, o *ops.Ops, w io.Writer, args []string) error {
	yes := false
	switch {
	case len(args) == 0:
	case len(args) == 1 && args[0] == "--yes":
		yes = true
	default:
		return usageError(i18n.T("takeover.usage"))
	}
	found, err := o.DetectForeign(ctx)
	if err != nil {
		return err
	}
	if len(found) == 0 {
		fmt.Fprintln(w, i18n.T("takeover.none"))
		return nil
	}
	for _, f := range found {
		printForeign(w, f)
		if !yes {
			printPlan(w, f)
			continue
		}
		r, err := o.Takeover(ctx, f)
		if len(r.Added) > 0 || (f.Hostlist != "" && err == nil) {
			fmt.Fprintln(w, "  "+i18n.T("takeover.imported", len(r.Added), f.Hostlist))
			printSkipped(w, "    ", r.Skipped)
		}
		if r.Stopped {
			fmt.Fprintln(w, "  "+i18n.T("takeover.did_stop", f.Service.Name))
		}
		if r.Disabled {
			fmt.Fprintln(w, "  "+i18n.T("takeover.did_disable", f.Service.Name))
		}
		if err != nil {
			return err
		}
		if f.Running && !r.Stopped {
			fmt.Fprintln(w, "  "+i18n.T("takeover.still_running", f.Kind))
		}
	}
	if !yes {
		fmt.Fprintln(w, i18n.T("takeover.untouched"))
		fmt.Fprintln(w, i18n.T("takeover.confirm", "sudo splitdpi takeover --yes"))
		return nil
	}
	fmt.Fprintln(w, i18n.T("takeover.done"))
	return nil
}

func printForeign(w io.Writer, f ops.Foreign) {
	fmt.Fprintln(w, i18n.T("takeover.found", f.Kind))
	if f.Dir != "" {
		fmt.Fprintln(w, "  "+i18n.T("takeover.dir", f.Dir))
	}
	if f.Service.Name == "" {
		fmt.Fprintln(w, "  "+i18n.T("takeover.no_service"))
	} else {
		fmt.Fprintln(w, "  "+i18n.T("takeover.service", f.Service.Name, string(f.Service.Init)))
		if f.Service.Enabled {
			fmt.Fprintln(w, "  "+i18n.T("takeover.boot"))
		}
	}
	if f.Running {
		fmt.Fprintln(w, "  "+i18n.T("takeover.running"))
	} else {
		fmt.Fprintln(w, "  "+i18n.T("takeover.not_running"))
	}
	if f.Hostlist != "" {
		fmt.Fprintln(w, "  "+i18n.T("takeover.hostlist", f.Hostlist))
	}
}

// printPlan mirrors what ops.Takeover does with f.
func printPlan(w io.Writer, f ops.Foreign) {
	fmt.Fprintln(w, "  "+i18n.T("takeover.plan"))
	var steps []string
	if f.Hostlist != "" {
		steps = append(steps, i18n.T("takeover.plan_import", f.Hostlist))
	}
	if f.Running && f.Service.Name != "" {
		steps = append(steps, i18n.T("takeover.plan_stop", f.Service.Name))
	}
	if f.Service.Enabled {
		steps = append(steps, i18n.T("takeover.plan_disable", f.Service.Name))
	}
	if f.Running && f.Service.Name == "" {
		steps = append(steps, i18n.T("takeover.plan_manual", f.Kind))
	}
	if len(steps) == 0 {
		steps = append(steps, i18n.T("takeover.plan_nothing"))
	}
	for _, s := range steps {
		fmt.Fprintln(w, "    - "+s)
	}
}
