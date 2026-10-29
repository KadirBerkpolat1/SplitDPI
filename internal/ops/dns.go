package ops

import (
	"context"
	"net"
	"net/netip"
	"net/url"

	"github.com/KadirBerkpolat1/SplitDPI/internal/config"
	"github.com/KadirBerkpolat1/SplitDPI/internal/presets"
	"github.com/KadirBerkpolat1/SplitDPI/internal/probe"
	"github.com/KadirBerkpolat1/SplitDPI/internal/supervisor"
)

// DNS turns the DoH redirect on or off in config. It applies on
// the next start; running reports whether an instance is running now.
func (o *Ops) DNS(on bool) (running bool, err error) {
	c, err := config.Load(o.P)
	if err != nil {
		return false, err
	}
	c.DNS = on
	if err := config.Save(o.P, c); err != nil {
		return false, err
	}
	_, _, held := supervisor.LockInfo(o.P)
	return held, nil
}

// ProbeDNS checks enabled apps' probe hostnames for poisoning.
func (o *Ops) ProbeDNS(ctx context.Context, apps []string) []probe.DNSResult {
	hosts := probeHosts(o.PresetsList(), apps)
	if len(hosts) == 0 {
		return nil
	}
	return probe.CheckDNS(ctx, hosts, o.lookupSystemDNS, func(ctx context.Context, name string) ([]netip.Addr, error) {
		return probe.ResolveDoH(ctx, o.HTTP, name)
	})
}

func (o *Ops) lookupSystemDNS(ctx context.Context, name string) ([]netip.Addr, error) {
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip", name)
	if err != nil {
		return nil, err
	}
	out := make([]netip.Addr, 0, len(ips))
	for _, ip := range ips {
		if a, ok := netip.AddrFromSlice(ip); ok {
			out = append(out, a)
		}
	}
	return out, nil
}

func probeHosts(set presets.Set, apps []string) []string {
	seen := map[string]bool{}
	var hosts []string
	for _, id := range apps {
		a, ok := set.Apps[id]
		if !ok {
			continue
		}
		for _, raw := range a.Probe {
			u, err := url.Parse(raw)
			if err != nil || u.Hostname() == "" {
				continue
			}
			h := u.Hostname()
			if !seen[h] {
				seen[h] = true
				hosts = append(hosts, h)
			}
		}
	}
	return hosts
}
