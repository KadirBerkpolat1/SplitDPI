package sysinfo

import (
	"context"
	"io/fs"
	"slices"
	"strings"

	"github.com/KadirBerkpolat1/SplitDPI/internal/execx"
)

// encryptedDaemons always send DNS upstream encrypted. comm is the process
// name as /proc/<pid>/stat shows it; units are the systemd units that run it,
// also when socket activation has not started the process yet.
var encryptedDaemons = []struct {
	name, comm string
	units      []string
}{
	{"dnscrypt-proxy", "dnscrypt-proxy", []string{"dnscrypt-proxy.service", "dnscrypt-proxy.socket"}},
	{"Stubby", "stubby", []string{"stubby.service"}},
	{"NextDNS", "nextdns", []string{"nextdns.service"}},
	{"AdGuard Home", "AdGuardHome", []string{"AdGuardHome.service"}},
}

// EncryptedResolver names the local resolver that already encrypts this
// machine's DNS, if any. comms are the names of the running processes. It
// only reads files under root and runs read-only queries, so it needs no root.
func EncryptedResolver(ctx context.Context, root fs.FS, run execx.Runner, comms []string) (name string, ok bool) {
	for _, d := range encryptedDaemons {
		if running(comms, d.comm) {
			return d.name, true
		}
	}
	if DetectInit(root) == Systemd {
		if name, ok := activeUnit(ctx, run); ok {
			return name, true
		}
	}
	if running(comms, "unbound") && unboundTLS(root) {
		return "Unbound", true
	}
	if running(comms, "kresd") && kresdTLS(root) {
		return "Knot Resolver", true
	}
	if running(comms, "systemd-resolved") && resolvedTLS(ctx, root, run) {
		return "systemd-resolved", true
	}
	return "", false
}

// running reports whether a process named exe runs; the kernel cuts process
// names to 15 bytes.
func running(comms []string, exe string) bool {
	return slices.Contains(comms, exe[:min(len(exe), 15)])
}

// activeUnit finds an encrypted daemon by its active systemd unit.
// systemctl prints one state per unit, and exits non-zero when any is not
// active, so its output is read whatever the error.
func activeUnit(ctx context.Context, run execx.Runner) (string, bool) {
	var units, names []string
	for _, d := range encryptedDaemons {
		for _, u := range d.units {
			units, names = append(units, u), append(names, d.name)
		}
	}
	out, _ := run(ctx, "", "systemctl", append([]string{"is-active"}, units...)...)
	for i, state := range strings.Fields(string(out)) {
		if i < len(names) && state == "active" {
			return names[i], true
		}
	}
	return "", false
}

// unboundTLS reports whether unbound forwards over TLS.
func unboundTLS(root fs.FS) bool {
	for _, line := range confLines(root, "#", "etc/unbound/unbound.conf", "etc/unbound/unbound.conf.d/*.conf") {
		k, v, ok := strings.Cut(line, ":")
		if ok && strings.TrimSpace(k) == "forward-tls-upstream" && strings.Trim(strings.TrimSpace(v), `"`) == "yes" {
			return true
		}
	}
	return false
}

// kresdTLS reports whether Knot Resolver forwards over TLS: policy.TLS_FORWARD
// in its Lua config (5.x), or transport: tls in its YAML config (6.x).
func kresdTLS(root fs.FS) bool {
	for _, line := range confLines(root, "--", "etc/knot-resolver/kresd.conf") {
		if strings.Contains(line, "TLS_FORWARD") {
			return true
		}
	}
	for _, line := range confLines(root, "#", "etc/knot-resolver/config.yaml") {
		if k, v, ok := strings.Cut(line, ":"); ok && strings.TrimSpace(k) == "transport" && strings.TrimSpace(v) == "tls" {
			return true
		}
	}
	return false
}

// resolvedTLS reports whether systemd-resolved uses DNS-over-TLS. resolvectl
// shows the live setting; only when it cannot be asked does the config count.
func resolvedTLS(ctx context.Context, root fs.FS, run execx.Runner) bool {
	if out, err := run(ctx, "", "resolvectl", "status"); err == nil {
		s := string(out)
		return strings.Contains(s, "+DNSOverTLS") || strings.Contains(s, "DNSOverTLS=opportunistic")
	}
	on, section := false, ""
	for _, line := range confLines(root, "#", "etc/systemd/resolved.conf", "etc/systemd/resolved.conf.d/*.conf") {
		if strings.HasPrefix(line, "[") {
			section = line
			continue
		}
		if k, v, ok := strings.Cut(line, "="); ok && section == "[Resolve]" && strings.TrimSpace(k) == "DNSOverTLS" {
			v = strings.ToLower(strings.TrimSpace(v))
			on = v == "yes" || v == "true" || v == "1" || v == "on" || v == "opportunistic"
		}
	}
	return on
}

// confLines returns the non-empty lines of the files matching patterns, in
// order, with comments starting at comment cut off.
func confLines(root fs.FS, comment string, patterns ...string) []string {
	var lines []string
	for _, pat := range patterns {
		files, _ := fs.Glob(root, pat)
		for _, f := range files {
			b, err := fs.ReadFile(root, f)
			if err != nil {
				continue
			}
			for line := range strings.Lines(string(b)) {
				line, _, _ = strings.Cut(line, comment)
				if line = strings.TrimSpace(line); line != "" {
					lines = append(lines, line)
				}
			}
		}
	}
	return lines
}
