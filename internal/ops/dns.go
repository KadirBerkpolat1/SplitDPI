package ops

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"net/url"
	"time"

	"github.com/KadirBerkpolat1/SplitDPI/internal/config"
	"github.com/KadirBerkpolat1/SplitDPI/internal/dns"
	"github.com/KadirBerkpolat1/SplitDPI/internal/doctor"
	"github.com/KadirBerkpolat1/SplitDPI/internal/i18n"
	"github.com/KadirBerkpolat1/SplitDPI/internal/presets"
	"github.com/KadirBerkpolat1/SplitDPI/internal/probe"
	"github.com/KadirBerkpolat1/SplitDPI/internal/supervisor"
)

// DNSProvider is one dns_provider choice as shown to the user.
type DNSProvider struct {
	ID   string
	Name string
	Note string
}

// DNSProviders lists every choice: off, auto, then each provider.
func (o *Ops) DNSProviders() []DNSProvider {
	out := []DNSProvider{
		{ID: dns.Off, Name: i18n.T("dns2.off_name"), Note: i18n.T("dns2.off_note")},
		{ID: dns.Auto, Name: i18n.T("dns2.auto_name"), Note: i18n.T("dns2.auto_note")},
	}
	for _, p := range dns.Providers {
		out = append(out, DNSProvider{ID: p.ID, Name: p.Name, Note: i18n.T("dns2.note_" + p.ID)})
	}
	return out
}

// DNSSetting is the configured encrypted DNS.
type DNSSetting struct {
	Provider string
	Protocol dns.Protocol
}

func (o *Ops) DNSSetting() (DNSSetting, error) {
	c, err := config.Load(o.P)
	if err != nil {
		return DNSSetting{}, err
	}
	return DNSSetting{Provider: c.DNSProvider, Protocol: c.DNSProtocol}, nil
}

// SetDNS saves the encrypted DNS choice. It applies on the next start;
// running reports whether an instance is running now.
func (o *Ops) SetDNS(provider string, proto dns.Protocol) (running bool, err error) {
	if !dns.Valid(provider) {
		return false, errors.New(i18n.T("dns2.unknown", provider))
	}
	if proto != dns.DoH && proto != dns.DoT {
		return false, errors.New(i18n.T("dns2.unknown", string(proto)))
	}
	c, err := config.Load(o.P)
	if err != nil {
		return false, err
	}
	c.DNSProvider, c.DNSProtocol = provider, proto
	if err := config.Save(o.P, c); err != nil {
		return false, err
	}
	_, _, held := supervisor.LockInfo(o.P)
	return held, nil
}

// SystemEncryptedDNS names the local resolver that already encrypts this
// machine's DNS, if any. SplitDPI leaves queries to local resolvers alone, so
// its own encrypted DNS then only serves programs that bypass that resolver.
func (o *Ops) SystemEncryptedDNS(ctx context.Context) (name string, ok bool) {
	return doctor.EncryptedDNS(ctx, o.doctorEnv())
}

// flushDNSCaches clears the local resolver caches (dns.FlushCaches) and
// writes what it did to log. A failure stops nothing: that cache keeps its
// answers until they expire.
func (o *Ops) flushDNSCaches(ctx context.Context, log io.Writer) {
	procs, err := o.procs()
	if err != nil {
		fmt.Fprintln(log, i18n.T("dns.cache_flush_failed", err))
		return
	}
	comms := make([]string, len(procs))
	for i, p := range procs {
		comms[i] = p.Comm
	}
	flushed, err := dns.FlushCaches(ctx, o.Exec, comms)
	for _, name := range flushed {
		fmt.Fprintln(log, i18n.T("dns.cache_flushed", name))
	}
	if err != nil {
		fmt.Fprintln(log, i18n.T("dns.cache_flush_failed", err))
	}
}

const flushTimeout = 10 * time.Second

// flushRunningDNS clears the local resolver caches after a site was added
// while SplitDPI runs with encrypted DNS in its config, so the site does not
// wait for an answer cached before to expire. Without the redirect a fresh
// answer would come from the ISP again, so then it does nothing.
func (o *Ops) flushRunningDNS() {
	if !o.Running() {
		return
	}
	if c, err := config.Load(o.P); err != nil || !c.DNSOn() {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), flushTimeout)
	defer cancel()
	o.flushDNSCaches(ctx, o.log())
}

// dnsClient resolves like the forwarder does with c; with DNS off, like auto
// over DoH, which is still a clean reference for poisoning checks.
func (o *Ops) dnsClient(c config.Config) *dns.Client {
	provider, proto := c.DNSProvider, c.DNSProtocol
	if !c.DNSOn() {
		provider, proto = dns.Auto, dns.DoH
	}
	return &dns.Client{Upstreams: dns.Upstreams(provider, proto), HTTP: o.HTTP}
}

// ProbeDNS checks enabled apps' probe hostnames for poisoning.
func (o *Ops) ProbeDNS(ctx context.Context, apps []string) []probe.DNSResult {
	hosts := probeHosts(o.PresetsList(), apps)
	if len(hosts) == 0 {
		return nil
	}
	return o.CheckDNS(ctx, hosts)
}

// CheckDNS compares the system resolver with encrypted DNS for each domain.
func (o *Ops) CheckDNS(ctx context.Context, domains []string) []probe.DNSResult {
	c, err := config.Load(o.P)
	if err != nil {
		c = config.Default()
	}
	return probe.CheckDNS(ctx, domains, lookupSystemDNS, o.dnsClient(c).Resolve)
}

var lookupSystemDNS = func(ctx context.Context, name string) ([]netip.Addr, error) {
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip", name)
	if err != nil {
		return nil, err
	}
	out := make([]netip.Addr, 0, len(ips))
	for _, ip := range ips {
		if a, ok := netip.AddrFromSlice(ip); ok {
			out = append(out, a.Unmap())
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
