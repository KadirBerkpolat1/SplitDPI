package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/KadirBerkpolat1/SplitDPI/internal/dns"
	"github.com/KadirBerkpolat1/SplitDPI/internal/i18n"
	"github.com/KadirBerkpolat1/SplitDPI/internal/ops"
)

func init() {
	commands["dns"] = Command{Name: "dns", Root: true, Help: "cmd.dns.help", Run: runDNS}
}

func runDNS(ctx context.Context, o *ops.Ops, w io.Writer, args []string) error {
	if len(args) == 0 {
		return printDNS(ctx, o, w)
	}
	provider, proto, err := parseDNSArgs(args)
	if err != nil {
		return usageError(i18n.T("dns.usage"))
	}
	running, err := o.SetDNS(provider, proto)
	if err != nil {
		return err
	}
	if provider == dns.Off {
		fmt.Fprintln(w, i18n.T("dns.off"))
	} else {
		p := dnsChoice(o, provider)
		fmt.Fprintln(w, i18n.T("dns2.set", p.Name, proto.Name()))
		if p.Note != "" {
			fmt.Fprintln(w, p.Note)
		}
		printSystemDNS(ctx, o, w)
		fmt.Fprintln(w, i18n.T("status.dns"))
	}
	if running {
		fmt.Fprintln(w, i18n.T("dns.restart"))
	}
	return nil
}

// parseDNSArgs reads `PROVIDER [--dot]`; `on` is auto, as in v0.1.
func parseDNSArgs(args []string) (string, dns.Protocol, error) {
	provider, proto := "", dns.DoH
	for _, a := range args {
		switch {
		case a == "--dot":
			proto = dns.DoT
		case a == "on" && provider == "":
			provider = dns.Auto
		case dns.Valid(a) && provider == "":
			provider = a
		default:
			return "", "", fmt.Errorf("bad argument %q", a)
		}
	}
	if provider == "" || provider == dns.Off && proto == dns.DoT {
		return "", "", fmt.Errorf("no provider")
	}
	return provider, proto, nil
}

func printDNS(ctx context.Context, o *ops.Ops, w io.Writer) error {
	s, err := o.DNSSetting()
	if err != nil {
		return err
	}
	if s.Provider == dns.Off {
		fmt.Fprintln(w, i18n.T("dns2.current_off"))
	} else {
		p := dnsChoice(o, s.Provider)
		fmt.Fprintln(w, i18n.T("dns2.current", p.Name, s.Protocol.Name()))
		if p.Note != "" {
			fmt.Fprintln(w, p.Note)
		}
	}
	printSystemDNS(ctx, o, w)
	fmt.Fprintln(w, i18n.T("dns.usage"))
	return nil
}

// printSystemDNS says when the system already encrypts DNS, and what
// SplitDPI's DNS still does then.
func printSystemDNS(ctx context.Context, o *ops.Ops, w io.Writer) {
	if name, ok := o.SystemEncryptedDNS(ctx); ok {
		fmt.Fprintln(w, i18n.T("dns_system.not_needed", name))
		fmt.Fprintln(w, i18n.T("dns_system.bypass_only"))
	}
}

func dnsChoice(o *ops.Ops, id string) ops.DNSProvider {
	for _, p := range o.DNSProviders() {
		if p.ID == id {
			return p
		}
	}
	return ops.DNSProvider{ID: id, Name: id}
}
