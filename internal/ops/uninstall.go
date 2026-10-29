package ops

import (
	"context"
	"errors"
	"os"
)

var ErrUninstallIncomplete = errors.New("uninstall incomplete")

// UninstallPlan is what Uninstall would remove: the installed service ("" when
// none) and the SplitDPI files and directories that exist, binary last.
type UninstallPlan struct {
	Service string
	Paths   []string
}

// UninstallReport lists what Uninstall removed and what is still present afterwards.
type UninstallReport RevertReport

// UninstallPlan reports what Uninstall would remove, without changing anything.
func (o *Ops) UninstallPlan() UninstallPlan {
	var p UninstallPlan
	if m, err := o.manager(); err == nil && m.Installed() {
		p.Service = serviceLabel(o)
	}
	for _, f := range append([]string{o.P.ConfigDir, o.P.StateDir, o.P.RunDir}, o.uninstallFiles()...) {
		if _, err := os.Lstat(f); err == nil {
			p.Paths = append(p.Paths, f)
		}
	}
	return p
}

// Uninstall does what revert --purge does, then removes the log and finally
// the binary at BinPath. The binary stays while anything else is left over,
// so uninstall can be run again.
func (o *Ops) Uninstall(ctx context.Context) (UninstallReport, error) {
	r := UninstallReport(o.Revert(ctx, false, true))
	for _, f := range o.uninstallFiles() {
		if _, err := os.Lstat(f); err != nil {
			continue
		}
		if f == o.P.BinPath && len(r.Leftover) > 0 {
			r.Leftover = append(r.Leftover, f)
		} else if err := os.Remove(f); err != nil {
			r.Leftover = append(r.Leftover, f)
		} else {
			r.Removed = append(r.Removed, f)
		}
	}
	if len(r.Leftover) > 0 {
		return r, ErrUninstallIncomplete
	}
	return r, nil
}

func (o *Ops) uninstallFiles() []string {
	return []string{o.P.LogFile, o.P.LogFile + ".1", o.P.BinPath}
}
