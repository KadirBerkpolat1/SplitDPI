package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/KadirBerkpolat1/SplitDPI/internal/i18n"
	"github.com/KadirBerkpolat1/SplitDPI/internal/ops"
)

func init() {
	commands["presets"] = Command{Name: "presets", Help: "cmd.presets.help", Run: runPresets}
	commands["apps"] = Command{Name: "apps", Root: true, Help: "cmd.apps.help", Run: runApps}
	commands["exclude"] = Command{Name: "exclude", Root: true, Help: "cmd.exclude.help", Run: runExclude}
	commands["sites"] = Command{Name: "sites", Root: true, Help: "cmd.sites.help", Run: runSites}
}

func runPresets(ctx context.Context, o *ops.Ops, w io.Writer, args []string) error {
	switch {
	case len(args) == 1 && args[0] == "update":
		return runPresetsUpdate(ctx, o, w, args[1:])
	case len(args) >= 1 && args[0] == "check":
		return runPresetsCheck(ctx, o, w, args[1:])
	case len(args) == 1 && args[0] == "list":
	default:
		return usageError(i18n.T("presets.usage"))
	}
	s := o.PresetsList()
	fmt.Fprintln(w, i18n.T("presets.header", s.Origin, s.Version))
	fmt.Fprintln(w, i18n.T("presets.isps"))
	for _, isp := range s.ISPs {
		asns := make([]string, len(isp.ASN))
		for i, a := range isp.ASN {
			asns[i] = "AS" + strconv.Itoa(a)
		}
		fmt.Fprintf(w, "  %s (%s, %s): %s\n", isp.Name, isp.Country, strings.Join(asns, " "), i18n.T("presets.strategies", len(isp.Strategies)))
	}
	fmt.Fprintln(w, i18n.T("presets.apps"))
	for _, id := range slices.Sorted(maps.Keys(s.Apps)) {
		fmt.Fprintf(w, "  %-10s %s\n", id, s.Apps[id].Name)
	}
	fmt.Fprintln(w, i18n.T("presets.countries", strings.Join(slices.Sorted(maps.Keys(s.Countries)), ", ")))
	fmt.Fprintln(w, i18n.T("presets.generic", len(s.Generic)))
	if len(s.Warnings) > 0 {
		fmt.Fprintln(w, i18n.T("presets.warnings"))
		for _, wn := range s.Warnings {
			fmt.Fprintln(w, "  "+wn)
		}
	}
	return nil
}

func runApps(_ context.Context, o *ops.Ops, w io.Writer, args []string) error {
	switch {
	case len(args) == 1 && args[0] == "list":
		apps, enabled, err := o.AppsList()
		if err != nil {
			return err
		}
		for _, a := range apps {
			mark := " "
			if slices.Contains(enabled, a.ID) {
				mark = "*"
			}
			fmt.Fprintf(w, "%s %-10s %s\n", mark, a.ID, a.Name)
			if a.Note != "" {
				fmt.Fprintln(w, "    "+a.Note)
			}
		}
		return nil
	case len(args) == 2 && args[0] == "enable":
		var unknown ops.UnknownAppError
		if err := o.AppsEnable(args[1]); errors.As(err, &unknown) {
			return errors.New(i18n.T("apps.unknown", unknown.ID, strings.Join(unknown.Valid, ", ")))
		} else if err != nil {
			return err
		}
		fmt.Fprintln(w, i18n.T("apps.enabled", args[1]))
		return nil
	case len(args) == 2 && args[0] == "disable":
		if err := o.AppsDisable(args[1]); err != nil {
			return err
		}
		fmt.Fprintln(w, i18n.T("apps.disabled", args[1]))
		return nil
	}
	return usageError(i18n.T("apps.usage"))
}

func runExclude(_ context.Context, o *ops.Ops, w io.Writer, args []string) error {
	switch {
	case len(args) == 1 && args[0] == "list":
		list, err := o.ExcludeList()
		if err != nil {
			return err
		}
		for _, d := range list {
			fmt.Fprintln(w, d)
		}
		return nil
	case len(args) == 2 && args[0] == "add":
		d, err := o.ExcludeAdd(args[1])
		if err != nil {
			return err
		}
		fmt.Fprintln(w, i18n.T("exclude.added", d))
		return nil
	case len(args) == 2 && args[0] == "remove":
		if err := o.ExcludeRemove(args[1]); err != nil {
			return err
		}
		fmt.Fprintln(w, i18n.T("exclude.removed", args[1]))
		return nil
	}
	return usageError(i18n.T("exclude.usage"))
}

func runSites(_ context.Context, o *ops.Ops, w io.Writer, args []string) error {
	switch {
	case len(args) == 1 && args[0] == "list":
		list, err := o.SitesList()
		if err != nil {
			return err
		}
		for _, d := range list {
			fmt.Fprintln(w, d)
		}
		return nil
	case len(args) == 2 && args[0] == "add":
		d, err := o.SitesAdd(args[1])
		if err != nil {
			return err
		}
		fmt.Fprintln(w, i18n.T("sites.added", d))
		return nil
	case len(args) == 2 && args[0] == "remove":
		if err := o.SitesRemove(args[1]); err != nil {
			return err
		}
		fmt.Fprintln(w, i18n.T("sites.removed", args[1]))
		return nil
	case len(args) == 2 && args[0] == "import":
		added, skipped, err := o.SitesImport(args[1])
		if err != nil {
			return err
		}
		printSkipped(w, "", skipped)
		fmt.Fprintln(w, i18n.T("sites_import.added", len(added)))
		return nil
	}
	return usageError(i18n.T("sites.usage"))
}

func printSkipped(w io.Writer, indent string, skipped []ops.ImportSkip) {
	for _, s := range skipped {
		fmt.Fprintln(w, indent+i18n.T("sites_import.skipped", s.Line, s.Text))
	}
}
