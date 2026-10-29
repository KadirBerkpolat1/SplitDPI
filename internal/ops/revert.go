package ops

import (
	"context"
	"os"
	"slices"
	"strings"

	"github.com/KadirBerkpolat1/SplitDPI/internal/nft"
	"github.com/KadirBerkpolat1/SplitDPI/internal/supervisor"
)

// RevertReport lists what revert removed and what is still present afterwards.
type RevertReport struct {
	Removed, Leftover []string
}

// Revert removes the service (stop, disable, remove files), stops a running
// supervisor, deletes our nft table and clears RunDir; purge also removes
// ConfigDir and StateDir. rulesOnly deletes only the table: it runs inside
// the service's own stop (ExecStopPost), where calling the service manager
// would deadlock. It uses only paths and the fixed table name, never config,
// so a corrupt config cannot block it.
func (o *Ops) Revert(ctx context.Context, rulesOnly, purge bool) RevertReport {
	var r RevertReport
	m, merr := o.manager()
	if !rulesOnly {
		if merr == nil && m.Installed() {
			if err := removeService(ctx, m); err != nil {
				r.Leftover = append(r.Leftover, serviceLabel(o)+": "+err.Error())
			} else {
				r.Removed = append(r.Removed, serviceLabel(o))
			}
		}
		if _, _, held := supervisor.LockInfo(o.P); held {
			if err := o.Stop(); err != nil {
				r.Leftover = append(r.Leftover, "splitdpi: "+err.Error())
			}
		}
	}

	table := "nft table inet " + o.P.NftTable
	existed, _ := nft.Exists(ctx, o.Exec, o.P.NftTable)
	if err := nft.Delete(ctx, o.Exec, o.P.NftTable); err == nil && existed {
		r.Removed = append(r.Removed, table)
	}
	var dirs []string
	if !rulesOnly {
		dirs = append(dirs, o.P.RunDir)
		if purge {
			dirs = append(dirs, o.P.ConfigDir, o.P.StateDir)
		}
	}
	for _, d := range dirs {
		if _, err := os.Stat(d); err == nil && os.RemoveAll(d) == nil {
			r.Removed = append(r.Removed, d)
		}
	}

	if ok, err := nft.Exists(ctx, o.Exec, o.P.NftTable); ok || err != nil {
		r.Leftover = append(r.Leftover, table)
	}
	if !rulesOnly && merr == nil && m.Installed() && !slices.ContainsFunc(r.Leftover, func(l string) bool {
		return strings.HasPrefix(l, serviceLabel(o))
	}) {
		r.Leftover = append(r.Leftover, serviceLabel(o))
	}
	for _, d := range dirs {
		if _, err := os.Stat(d); !os.IsNotExist(err) {
			r.Leftover = append(r.Leftover, d)
		}
	}
	return r
}
