package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/KadirBerkpolat1/SplitDPI/internal/config"
	"github.com/KadirBerkpolat1/SplitDPI/internal/i18n"
	"github.com/KadirBerkpolat1/SplitDPI/internal/ops"
)

func init() {
	commands["probe"] = Command{Name: "probe", Root: true, Help: "cmd.probe.help", Run: runProbe}
}

func runProbe(ctx context.Context, o *ops.Ops, w io.Writer, args []string) error {
	apps, err := parseProbeArgs(args)
	if err != nil {
		return usageError(i18n.T("probe.usage"))
	}
	if dnsApps, _ := appsForProbe(o, apps); len(dnsApps) > 0 {
		for _, r := range o.ProbeDNS(ctx, dnsApps) {
			switch {
			case r.Poisoned:
				fmt.Fprintf(w, "dns  %s  ✗  (%s)\n", r.Domain, r.Reason)
			case r.Mismatch:
				fmt.Fprintf(w, "dns  %s  ~  (%s)\n", r.Domain, i18n.T("probe.dns.mismatch"))
			default:
				fmt.Fprintf(w, "dns  %s  ✓\n", r.Domain)
			}
		}
	}
	results, err := o.Probe(ctx, apps)
	if err != nil {
		var noApps ops.NoAppsError
		if errors.As(err, &noApps) {
			return err
		}
		var unknown ops.UnknownAppError
		if errors.As(err, &unknown) {
			return errors.New(i18n.T("apps.unknown", unknown.ID, strings.Join(unknown.Valid, ", ")))
		}
		return err
	}
	var failed bool
	for app, rs := range results {
		for _, r := range rs {
			mark := "✗"
			if r.OK {
				mark = "✓"
			} else {
				failed = true
			}
			line := fmt.Sprintf("%s  %s  %s", app, r.URL, mark)
			if !r.OK && r.Err != "" {
				line += "  (" + r.Err + ")"
			}
			fmt.Fprintln(w, line)
		}
	}
	if failed {
		return errors.New(i18n.T("probe.failed"))
	}
	return nil
}

func parseProbeArgs(args []string) ([]string, error) {
	if len(args) == 0 {
		return nil, nil
	}
	if len(args) == 1 && args[0] == "--apps" {
		return nil, fmt.Errorf("missing value")
	}
	if len(args) == 2 && args[0] == "--apps" {
		var apps []string
		for _, a := range strings.Split(args[1], ",") {
			a = strings.TrimSpace(a)
			if a == "" {
				continue
			}
			apps = append(apps, a)
		}
		if len(apps) == 0 {
			return nil, fmt.Errorf("empty")
		}
		return apps, nil
	}
	return nil, fmt.Errorf("usage")
}

func appsForProbe(o *ops.Ops, apps []string) ([]string, error) {
	if len(apps) > 0 {
		return apps, nil
	}
	c, err := config.Load(o.P)
	if err != nil {
		return nil, err
	}
	return c.Apps, nil
}
