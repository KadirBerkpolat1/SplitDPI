package ops

import (
	"context"
	"errors"
	"os/exec"
	"slices"
	"strings"

	"github.com/KadirBerkpolat1/SplitDPI/internal/i18n"
	"github.com/KadirBerkpolat1/SplitDPI/internal/sysinfo"
)

func (o *Ops) lookPath(name string) (string, error) {
	if o.LookPath != nil {
		return o.LookPath(name)
	}
	return exec.LookPath(name)
}

func (o *Ops) pkgManager() sysinfo.PkgManager {
	if o.Root == nil {
		return sysinfo.Unknown
	}
	return sysinfo.DetectPkgManager(sysinfo.ReadOSRelease(o.Root), o.Root)
}

// MissingPackages are the packages that install the tools this machine
// lacks, out of tools; with none given, out of every tool splitdpi needs.
func (o *Ops) MissingPackages(tools ...sysinfo.Tool) sysinfo.Packages {
	if len(tools) == 0 {
		tools = sysinfo.Tools
	}
	return sysinfo.NewPackages(o.pkgManager(), o.missing(tools))
}

func (o *Ops) missing(tools []sysinfo.Tool) []sysinfo.Tool {
	var out []sysinfo.Tool
	for _, t := range tools {
		found := slices.ContainsFunc(t.Commands, func(name string) bool {
			_, err := o.lookPath(name)
			return err == nil
		})
		if !found {
			out = append(out, t)
		}
	}
	return out
}

// InstallError is a package manager that failed. Refresh is the failed
// package list update before it, if any; Hint installs the packages by hand.
type InstallError struct {
	Err     error
	Refresh error
	Hint    string
}

func (e InstallError) Error() string {
	return i18n.T("deps.failed", errors.Join(e.Err, e.Refresh), e.Hint)
}

func (e InstallError) Unwrap() error { return e.Err }

// InstallPackages installs p with the package manager, then checks that its
// tools are on PATH.
func (o *Ops) InstallPackages(ctx context.Context, p sysinfo.Packages) error {
	if len(p.Tools) == 0 {
		return nil
	}
	c, ok := sysinfo.Installer(p.Manager, p.Names)
	if !ok {
		return errors.New(i18n.T("deps.unknown_pm", strings.Join(p.Names, ", ")))
	}
	var refreshErr error
	if len(c.Refresh) > 0 {
		_, refreshErr = o.Exec(ctx, "", c.Refresh[0], c.Refresh[1:]...)
	}
	if _, err := o.Exec(ctx, "", c.Install[0], c.Install[1:]...); err != nil {
		return InstallError{Err: err, Refresh: refreshErr, Hint: p.RetryHint()}
	}
	if still := sysinfo.NewPackages(p.Manager, o.missing(p.Tools)); len(still.Tools) > 0 {
		return errors.New(i18n.T("deps.still_missing", strings.Join(still.Names, ", ")))
	}
	return nil
}

// InstallHint is how the user installs p: splitdpi's own command where it
// knows the package manager.
func InstallHint(p sysinfo.Packages) string {
	if p.CanInstall() {
		return "sudo splitdpi deps install"
	}
	return p.Hint()
}
