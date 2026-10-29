package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"

	"github.com/KadirBerkpolat1/SplitDPI/internal/config"
	"github.com/KadirBerkpolat1/SplitDPI/internal/i18n"
	"github.com/KadirBerkpolat1/SplitDPI/internal/ops"
	"github.com/KadirBerkpolat1/SplitDPI/internal/probe"
)

func init() {
	commands["probe"] = Command{Name: "probe", Root: true, Help: "cmd.probe.help", Run: runProbe}
}

func runProbe(ctx context.Context, o *ops.Ops, w io.Writer, args []string) error {
	apps, verbose, err := parseProbeArgs(args)
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
	return printProbe(w, results, verbose)
}

// printProbe lists each result and counts the apps and sites that open, are
// blocked, or have no address to test. Only a blocked one is an error.
func printProbe(w io.Writer, results map[string][]probe.Result, verbose bool) error {
	var open, blocked, untested int
	for _, app := range slices.Sorted(maps.Keys(results)) {
		for _, r := range results[app] {
			cat := probe.Classify(r)
			switch {
			case cat == probe.None:
				fmt.Fprintf(w, "%s  %s  ✓\n", app, r.URL)
				continue
			case cat == probe.NoAddress:
				fmt.Fprintf(w, "%s  %s  -  %s\n", app, r.URL, cat.What())
			default:
				fmt.Fprintf(w, "%s  %s  ✗  %s\n", app, r.URL, cat.What())
			}
			fmt.Fprintln(w, "    "+cat.Todo())
			printRawErr(w, r, verbose)
		}
		switch probe.Overall(results[app]) {
		case probe.None:
			open++
		case probe.NoAddress:
			untested++
		default:
			blocked++
		}
	}
	fmt.Fprintln(w, i18n.T("probe_summary.cli", open, blocked, untested))
	if blocked > 0 {
		return errors.New(i18n.T("probe.failed"))
	}
	return nil
}

// printRawErr shows the error behind a failed probe, only when asked for.
func printRawErr(w io.Writer, r probe.Result, verbose bool) {
	if verbose && r.Err != "" {
		fmt.Fprintln(w, "    "+r.Err)
	}
}

func parseProbeArgs(args []string) (apps []string, verbose bool, err error) {
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-v", "--verbose":
			verbose = true
		case "--apps":
			if i+1 >= len(args) || apps != nil {
				return nil, false, fmt.Errorf("usage")
			}
			i++
			for _, a := range strings.Split(args[i], ",") {
				if a = strings.TrimSpace(a); a != "" {
					apps = append(apps, a)
				}
			}
			if len(apps) == 0 {
				return nil, false, fmt.Errorf("empty")
			}
		default:
			return nil, false, fmt.Errorf("usage")
		}
	}
	return apps, verbose, nil
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
