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
	"github.com/KadirBerkpolat1/SplitDPI/internal/presets"
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
			fmt.Fprintf(w, "%s %-10s %s%s\n", mark, a.ID, a.Name, appKind(a))
			if note := ops.AppNote(a); note != "" {
				fmt.Fprintln(w, "    "+note)
			}
		}
		return nil
	case len(args) == 2 && args[0] == "show":
		a, err := o.App(args[1])
		if err != nil {
			return appErr(err)
		}
		fmt.Fprintf(w, "%s (%s)%s\n", a.Name, a.ID, appKind(a))
		fmt.Fprintln(w, i18n.T("apps.hosts"))
		for _, h := range a.Hosts {
			fmt.Fprintln(w, "  "+h)
		}
		switch {
		case a.UDP != nil:
			fmt.Fprintln(w, i18n.T("apps.udp_state", i18n.T("apps.on")))
		case a.Preset != nil && a.Preset.UDP != nil:
			fmt.Fprintln(w, i18n.T("apps.udp_state", i18n.T("apps.off")))
		}
		return nil
	case len(args) >= 2 && args[0] == "new":
		if err := o.AppNew(args[1], strings.Join(args[2:], " ")); err != nil {
			return appErr(err)
		}
		fmt.Fprintln(w, i18n.T("apps.created", args[1]))
		return nil
	case len(args) == 3 && args[0] == "add-host":
		d, err := o.AppHostAdd(args[1], args[2])
		if err != nil {
			return appErr(err)
		}
		fmt.Fprintln(w, i18n.T("apps.host_added", d, args[1]))
		return nil
	case len(args) == 3 && args[0] == "remove-host":
		if err := o.AppHostRemove(args[1], args[2]); err != nil {
			return appErr(err)
		}
		fmt.Fprintln(w, i18n.T("apps.host_removed", args[2], args[1]))
		return nil
	case len(args) == 3 && args[0] == "udp" && (args[2] == "on" || args[2] == "off"):
		on := args[2] == "on"
		if err := o.AppSetUDP(args[1], on); err != nil {
			return appErr(err)
		}
		msg := "apps.udp_off"
		if on {
			msg = "apps.udp_on"
		}
		fmt.Fprintln(w, i18n.T(msg, args[1]))
		return nil
	case len(args) == 2 && args[0] == "reset":
		a, err := o.App(args[1])
		if err == nil {
			err = o.AppReset(args[1])
		}
		if err != nil {
			return appErr(err)
		}
		msg := "apps.reset"
		if a.User && a.Preset == nil {
			msg = "apps.deleted"
		}
		fmt.Fprintln(w, i18n.T(msg, args[1]))
		return nil
	case len(args) == 2 && args[0] == "enable":
		if err := o.AppsEnable(args[1]); err != nil {
			return appErr(err)
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

// appKind says after an app's name whether it is the user's copy of a
// shipped app or an app of the user's own.
func appKind(a presets.App) string {
	switch {
	case a.User && a.Preset != nil:
		return ", " + i18n.T("shell.sites.app_edited")
	case a.User:
		return ", " + i18n.T("shell.sites.app_yours")
	}
	return ""
}

// appErr words the app errors for the user.
func appErr(err error) error {
	var unknown ops.UnknownAppError
	var exists ops.AppExistsError
	switch {
	case errors.As(err, &unknown):
		return errors.New(i18n.T("apps.unknown", unknown.ID, strings.Join(unknown.Valid, ", ")))
	case errors.As(err, &exists):
		return errors.New(i18n.T("apps.exists", exists.ID))
	case errors.Is(err, ops.ErrNoUDP):
		return errors.New(i18n.T("apps.no_udp"))
	}
	return err
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
		fmt.Fprintln(w, i18n.N("sites_import.added", len(added)))
		return nil
	}
	return usageError(i18n.T("sites.usage"))
}

func printSkipped(w io.Writer, indent string, skipped []ops.ImportSkip) {
	for _, s := range skipped {
		fmt.Fprintln(w, indent+i18n.T("sites_import.skipped", s.Line, s.Text))
	}
}
