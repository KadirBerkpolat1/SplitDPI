package ops

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/KadirBerkpolat1/SplitDPI/internal/config"
	"github.com/KadirBerkpolat1/SplitDPI/internal/dns"
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
	var preErr error // stopped blockcheck2 before it ran
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
		// Trials after this see c, so a DNS turned on here serves them too.
		Blockcheck: func(ctx context.Context, p scan.BlockcheckParams, prog func(string)) (string, error) {
			if preErr = o.scanPackages(ctx, opt, progress); preErr != nil {
				return "", preErr
			}
			if preErr = o.scanDNS(ctx, &c, opt, p.Domains, progress); preErr != nil {
				return "", preErr
			}
			if c.DNSOn() {
				for _, u := range dns.Upstreams(c.DNSProvider, dns.DoH) {
					p.DoH = append(p.DoH, u.URL())
				}
			}
			return scan.RunBlockcheck(ctx, eng, o.Exec, p, prog)
		},
		IPv6:      o.HasIPv6(ctx),
		Now:       o.Now,
		Progress:  progress,
		EngineVer: engine.Version,
	}
	rep, err := scan.Scan(ctx, env, opt)
	if preErr != nil {
		return rep, preErr
	}
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

// scanPackages makes sure blockcheck2 finds curl and nslookup or host, which
// it cannot run without, installing them when opt.Packages agrees.
func (o *Ops) scanPackages(ctx context.Context, opt scan.Options, progress func(string)) error {
	p := o.MissingPackages(sysinfo.Curl, sysinfo.Lookup)
	if len(p.Tools) == 0 {
		return nil
	}
	names := strings.Join(p.Names, ", ")
	if !p.CanInstall() || opt.Packages == nil || !opt.Packages(ctx, p) {
		return errors.New(i18n.T("scan.packages", names, InstallHint(p)))
	}
	if progress != nil {
		progress(i18n.T("deps.installing", names, string(p.Manager)))
	}
	if err := o.InstallPackages(ctx, p); err != nil {
		return err
	}
	if progress != nil {
		progress(i18n.T("deps.installed", names))
	}
	return nil
}

// scanDNS runs opt.DNSCheck when DNS is off and some of domains look
// poisoned, and turns on the provider it returns, in c and in config. When
// the system already encrypts DNS, it only says so, unless opt.DNSForce.
func (o *Ops) scanDNS(ctx context.Context, c *config.Config, opt scan.Options, domains []string, progress func(string)) error {
	ask := opt.DNSCheck
	if ask == nil || c.DNSOn() {
		return nil
	}
	if !opt.DNSForce {
		if name, ok := o.SystemEncryptedDNS(ctx); ok {
			if progress != nil {
				progress(i18n.T("dns_system.scan_skip", name))
			}
			return nil
		}
	}
	var poisoned []probe.DNSResult
	for _, r := range o.CheckDNS(ctx, domains) {
		if r.Poisoned {
			poisoned = append(poisoned, r)
		}
	}
	if len(poisoned) == 0 {
		return nil
	}
	provider := ask(ctx, poisoned)
	if provider == "" || provider == dns.Off {
		return nil
	}
	if _, err := o.SetDNS(provider, dns.DoH); err != nil {
		return err
	}
	c.DNSProvider, c.DNSProtocol = provider, dns.DoH
	return nil
}

// HasIPv6 reports whether scans test IPv6 as well as IPv4.
func (o *Ops) HasIPv6(ctx context.Context) bool {
	return sysinfo.HasGlobalIPv6(ctx, o.Exec)
}
