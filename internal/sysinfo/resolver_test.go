package sysinfo

import (
	"context"
	"errors"
	"strings"
	"testing"
	"testing/fstest"
)

// resolverRun fakes systemctl and resolvectl; a missing entry fails like a
// command that is not installed.
func resolverRun(out map[string]string) func(context.Context, string, string, ...string) ([]byte, error) {
	return func(_ context.Context, _ string, name string, args ...string) ([]byte, error) {
		key := name
		if len(args) > 0 {
			key += " " + args[0]
		}
		s, ok := out[key]
		if !ok {
			return nil, errors.New(name + ": not found")
		}
		if name == "systemctl" && strings.Contains(s, "inactive") {
			return []byte(s), errors.New("systemctl: exit status 3")
		}
		return []byte(s), nil
	}
}

func TestEncryptedResolver(t *testing.T) {
	systemd := func(files fstest.MapFS) fstest.MapFS {
		files["run/systemd/system/.keep"] = &fstest.MapFile{}
		return files
	}
	const (
		inactive       = "inactive\ninactive\ninactive\ninactive\ninactive\n"
		socketOnly     = "inactive\nactive\ninactive\ninactive\ninactive\n"
		resolvedPlain  = "Global\n       Protocols: -LLMNR -mDNS -DNSOverTLS DNSSEC=no/unsupported\nresolv.conf mode: stub\n"
		resolvedDoT    = "Global\n       Protocols: -LLMNR -mDNS +DNSOverTLS DNSSEC=no/unsupported\n"
		resolvedOpport = "Global\n       Protocols: +LLMNR +mDNS DNSOverTLS=opportunistic DNSSEC=no/unsupported\n"
	)
	for _, tc := range []struct {
		name  string
		root  fstest.MapFS
		out   map[string]string
		comms []string
		want  string
	}{
		{name: "nothing", root: fstest.MapFS{}, comms: []string{"systemd", "bash"}},
		{name: "dnscrypt-proxy process", root: fstest.MapFS{}, comms: []string{"bash", "dnscrypt-proxy"}, want: "dnscrypt-proxy"},
		{name: "stubby process", root: fstest.MapFS{}, comms: []string{"stubby"}, want: "Stubby"},
		{name: "nextdns process", root: fstest.MapFS{}, comms: []string{"nextdns"}, want: "NextDNS"},
		{name: "AdGuard Home process", root: fstest.MapFS{}, comms: []string{"AdGuardHome"}, want: "AdGuard Home"},
		{name: "dnscrypt-proxy socket only", root: systemd(fstest.MapFS{}), out: map[string]string{"systemctl is-active": socketOnly}, want: "dnscrypt-proxy"},
		{name: "no unit active", root: systemd(fstest.MapFS{}), out: map[string]string{"systemctl is-active": inactive, "resolvectl status": resolvedPlain}},
		{name: "units not asked without systemd", root: fstest.MapFS{}, out: map[string]string{"systemctl is-active": socketOnly}},
		{
			name:  "unbound forwarding over TLS",
			root:  fstest.MapFS{"etc/unbound/unbound.conf.d/fwd.conf": file("forward-zone:\n  name: \".\"\n  forward-tls-upstream: yes # DoT\n  forward-addr: 9.9.9.9@853\n")},
			comms: []string{"unbound"},
			want:  "Unbound",
		},
		{
			name:  "unbound recursing in plain",
			root:  fstest.MapFS{"etc/unbound/unbound.conf": file("server:\n  # forward-tls-upstream: yes\n  interface: 127.0.0.1\n")},
			comms: []string{"unbound"},
		},
		{
			name: "unbound TLS config but not running",
			root: fstest.MapFS{"etc/unbound/unbound.conf": file("forward-zone:\n  forward-tls-upstream: yes\n")},
		},
		{
			name:  "kresd TLS_FORWARD",
			root:  fstest.MapFS{"etc/knot-resolver/kresd.conf": file("policy.add(policy.all(policy.TLS_FORWARD({{'1.1.1.1', hostname='cloudflare-dns.com'}})))\n")},
			comms: []string{"kresd"},
			want:  "Knot Resolver",
		},
		{
			name:  "kresd 6 transport tls",
			root:  fstest.MapFS{"etc/knot-resolver/config.yaml": file("forward:\n  - subtree: .\n    servers:\n      - address: [ 1.1.1.1 ]\n        transport: tls\n")},
			comms: []string{"kresd"},
			want:  "Knot Resolver",
		},
		{
			name:  "kresd TLS_FORWARD only in a comment",
			root:  fstest.MapFS{"etc/knot-resolver/kresd.conf": file("-- policy.add(policy.all(policy.TLS_FORWARD({})))\nnet.tls('/cert.pem', '/key.pem')\n")},
			comms: []string{"kresd"},
		},
		{name: "resolved with DoT", root: fstest.MapFS{}, out: map[string]string{"resolvectl status": resolvedDoT}, comms: []string{"systemd-resolve"}, want: "systemd-resolved"},
		{name: "resolved with opportunistic DoT", root: fstest.MapFS{}, out: map[string]string{"resolvectl status": resolvedOpport}, comms: []string{"systemd-resolve"}, want: "systemd-resolved"},
		{name: "resolved without DoT", root: fstest.MapFS{}, out: map[string]string{"resolvectl status": resolvedPlain}, comms: []string{"systemd-resolve"}},
		{
			name:  "resolvectl wins over the config",
			root:  fstest.MapFS{"etc/systemd/resolved.conf": file("[Resolve]\nDNSOverTLS=yes\n")},
			out:   map[string]string{"resolvectl status": resolvedPlain},
			comms: []string{"systemd-resolve"},
		},
		{
			name:  "resolved DoT in a drop-in",
			root:  fstest.MapFS{"etc/systemd/resolved.conf": file("[Resolve]\n#DNSOverTLS=no\n"), "etc/systemd/resolved.conf.d/dot.conf": file("[Resolve]\nDNS=9.9.9.9#dns.quad9.net\nDNSOverTLS=opportunistic\n")},
			comms: []string{"systemd-resolve"},
			want:  "systemd-resolved",
		},
		{
			name:  "resolved drop-in turns DoT off",
			root:  fstest.MapFS{"etc/systemd/resolved.conf": file("[Resolve]\nDNSOverTLS=yes\n"), "etc/systemd/resolved.conf.d/off.conf": file("[Resolve]\nDNSOverTLS=no\n")},
			comms: []string{"systemd-resolve"},
		},
		{
			name:  "resolved config without DoT",
			root:  fstest.MapFS{"etc/systemd/resolved.conf": file("[Resolve]\nDNS=1.1.1.1\n#DNSOverTLS=yes\n")},
			comms: []string{"systemd-resolve"},
		},
		{
			name: "resolved DoT config but not running",
			root: fstest.MapFS{"etc/systemd/resolved.conf": file("[Resolve]\nDNSOverTLS=yes\n")},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			name, ok := EncryptedResolver(context.Background(), tc.root, resolverRun(tc.out), tc.comms)
			if name != tc.want || ok != (tc.want != "") {
				t.Errorf("got %q %v, want %q", name, ok, tc.want)
			}
		})
	}
}
