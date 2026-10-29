package ops

import (
	"context"
	"fmt"

	"github.com/KadirBerkpolat1/SplitDPI/internal/config"
	"github.com/KadirBerkpolat1/SplitDPI/internal/domain"
	"github.com/KadirBerkpolat1/SplitDPI/internal/engine"
	"github.com/KadirBerkpolat1/SplitDPI/internal/i18n"
	"github.com/KadirBerkpolat1/SplitDPI/internal/presets"
	"github.com/KadirBerkpolat1/SplitDPI/internal/probe"
	"github.com/KadirBerkpolat1/SplitDPI/internal/scan"
	"github.com/KadirBerkpolat1/SplitDPI/internal/supervisor"
	"github.com/KadirBerkpolat1/SplitDPI/internal/sysinfo"
)

// RunLockError means splitdpi is already running.
type RunLockError struct{}

func (RunLockError) Error() string { return i18n.T("scan.locked") }

// Scan finds a working strategy and saves it to config. With opt.Site, the
// site is added to the user sites too, so the strategy applies to it.
func (o *Ops) Scan(ctx context.Context, opt scan.Options, progress func(string)) (scan.Report, error) {
	if opt.Site != "" {
		n, err := domain.Normalize(opt.Site)
		if err != nil {
			return scan.Report{}, err
		}
		opt.Site = n
	}
	if _, _, held := supervisor.LockInfo(o.P); held {
		return scan.Report{}, RunLockError{}
	}
	// Another bypass tool would skew both preset trials and blockcheck2.
	if err := o.refuseConflicts(ctx); err != nil {
		return scan.Report{}, err
	}
	c, err := config.Load(o.P)
	if err != nil {
		return scan.Report{}, err
	}
	if len(opt.Apps) == 0 {
		opt.Apps = c.Apps
	}
	if opt.Sites, err = o.SitesList(); err != nil {
		return scan.Report{}, err
	}
	if len(opt.Apps) == 0 && len(opt.Sites) == 0 && opt.Site == "" {
		return scan.Report{}, NoAppsError{}
	}
	eng, ok := engine.Installed(o.P)
	if !ok {
		return scan.Report{}, fmt.Errorf("%s", i18n.T("engine.not_installed"))
	}
	set := o.PresetsList()
	// The scanned site joins the user sites only once a strategy is saved, but
	// trials must already handle it from the first packet.
	var trialHosts []string
	if opt.Site != "" {
		trialHosts = []string{opt.Site}
	}
	env := scan.Env{
		Set: set,
		Lookup: func(ctx context.Context) (scan.ASNInfo, error) {
			info, _, err := o.Detect(ctx, opt.ASN, opt.Country)
			return info, err
		},
		StartTrial: func(ctx context.Context, st presets.Strategy) (func(), error) {
			return o.startTrial(ctx, c, st, trialHosts)
		},
		Probe: func(ctx context.Context, urls []string) []probe.Result {
			return probe.Prober{Client: o.HTTP, Timeout: probe.DefaultTimeout}.Probe(ctx, urls)
		},
		Blockcheck: func(ctx context.Context, p scan.BlockcheckParams, prog func(string)) (string, error) {
			return scan.RunBlockcheck(ctx, eng, o.Exec, p, prog)
		},
		IPv6:      o.HasIPv6(ctx),
		Now:       o.Now,
		Progress:  progress,
		EngineVer: engine.Version,
	}
	rep, err := scan.Scan(ctx, env, opt)
	if err != nil {
		return rep, err
	}
	if rep.Winner != nil {
		c.Strategy = rep.Winner
		if err := config.Save(o.P, c); err != nil {
			return rep, err
		}
		if opt.Site != "" {
			if _, err := o.SitesAdd(opt.Site); err != nil {
				return rep, err
			}
		}
	}
	return rep, nil
}

// HasIPv6 reports whether scans test IPv6 as well as IPv4.
func (o *Ops) HasIPv6(ctx context.Context) bool {
	return sysinfo.HasGlobalIPv6(ctx, o.Exec)
}
