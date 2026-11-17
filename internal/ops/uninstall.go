package ops

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
)

var ErrUninstallIncomplete = errors.New("uninstall incomplete")

// UninstallPlan is what Uninstall would remove: the installed service ("" when
// none) and the SplitDPI files and directories that exist, binaries last.
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
	for _, f := range slices.Concat([]string{o.P.ConfigDir, o.P.StateDir, o.P.RunDir}, o.logFiles(), o.binaries()) {
		if _, err := os.Lstat(f); err == nil {
			p.Paths = append(p.Paths, f)
		}
	}
	return p
}

// Uninstall does what revert --purge does, then removes the log and finally
// the binaries. They stay while anything else is left over, so uninstall
// can be run again.
func (o *Ops) Uninstall(ctx context.Context) (UninstallReport, error) {
	bins := o.binaries()
	r := UninstallReport(o.Revert(ctx, false, true))
	for _, f := range slices.Concat(o.logFiles(), bins) {
		if _, err := os.Lstat(f); err != nil {
			continue
		}
		if slices.Contains(bins, f) && len(r.Leftover) > 0 {
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

func (o *Ops) logFiles() []string {
	return []string{o.P.LogFile, o.P.LogFile + ".1"}
}

// binaries are the splitdpi binaries to remove: BinPath, and this binary
// when only root can change it, as one install.sh put under another prefix
// that the service ran.
func (o *Ops) binaries() []string {
	bins := []string{o.P.BinPath}
	if self, err := o.selfBinary(); err == nil {
		if f := filepath.Join(o.P.Root, self); f != o.P.BinPath {
			bins = append(bins, f)
		}
	}
	return bins
}
