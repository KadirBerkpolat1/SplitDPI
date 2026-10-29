package cli

import (
	"bytes"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/KadirBerkpolat1/SplitDPI/internal/config"
	"github.com/KadirBerkpolat1/SplitDPI/internal/engine"
	"github.com/KadirBerkpolat1/SplitDPI/internal/ops"
	"github.com/KadirBerkpolat1/SplitDPI/internal/presets"
	"github.com/KadirBerkpolat1/SplitDPI/internal/probe"
	"github.com/KadirBerkpolat1/SplitDPI/internal/scan"
)

func TestParseScanSite(t *testing.T) {
	opt, verbose, dnsFlag, err := parseScanArgs([]string{"--site", "pastebin.com", "--asn", "9121"})
	if err != nil || opt.Site != "pastebin.com" || opt.ASN != 9121 || verbose || dnsFlag != "" {
		t.Fatalf("got %+v, %v, %q, %v", opt, verbose, dnsFlag, err)
	}
	if _, verbose, _, err := parseScanArgs([]string{"-v", "--no-blockcheck"}); err != nil || !verbose {
		t.Errorf("-v: verbose %v, %v", verbose, err)
	}
	for _, args := range [][]string{{"--site"}, {"--site", "pastebin.com", "--no-blockcheck"}} {
		if _, _, _, err := parseScanArgs(args); err == nil {
			t.Errorf("%q accepted", args)
		}
	}
}

func TestParseScanDNS(t *testing.T) {
	for _, v := range []string{"keep", "auto", "cloudflare", "adguard"} {
		if _, _, got, err := parseScanArgs([]string{"--dns", v}); err != nil || got != v {
			t.Errorf("--dns %s: %q, %v", v, got, err)
		}
	}
	for _, args := range [][]string{{"--dns"}, {"--dns", "off"}, {"--dns", "on"}, {"--dns", "8.8.8.8"}} {
		if _, _, _, err := parseScanArgs(args); err == nil {
			t.Errorf("%q accepted", args)
		}
	}
}

func TestPrintTrialsFriendly(t *testing.T) {
	trials := []scan.Trial{{
		Candidate: presets.Candidate{Strategy: presets.Strategy{ID: "tt-1"}, Source: "isp/tr/x.toml"},
		Results: []probe.Result{
			{URL: "https://discord.com/", OK: true},
			{URL: "https://web.telegram.org/", Err: "read: connection reset by peer", Cause: syscall.ECONNRESET},
		},
	}}
	var b strings.Builder
	printTrials(&b, trials, false)
	has(t, b.String(), "https://web.telegram.org/  "+probe.Reset.What())
	if strings.Contains(b.String(), "connection reset by peer") {
		t.Errorf("raw error without -v:\n%s", b.String())
	}
	b.Reset()
	printTrials(&b, trials, true)
	has(t, b.String(), "    read: connection reset by peer")
}

func TestScanSiteUsageAndBadDomain(t *testing.T) {
	if out, code := run(t, 0, "scan", "--site", "pastebin.com", "--no-blockcheck"); code != 2 {
		t.Errorf("--site with --no-blockcheck: code %d, want 2:\n%s", code, out)
	}
	if _, code := run(t, 0, "scan", "--site", "1.2.3.4"); code != 1 {
		t.Errorf("bad domain: code %d, want 1", code)
	}
	out, code := run(t, 1000, "scan", "--site", "pastebin.com")
	eq(t, code, 1)
	has(t, out, "sudo splitdpi scan --site pastebin.com")
}

// With dnscrypt-proxy encrypting DNS, a scan says so instead of checking for
// poisoning and asking, even on a terminal that would answer yes.
func TestScanSkipsDNSQuestionWhenSystemEncrypts(t *testing.T) {
	o, _ := runOpsFor(t, dnscryptRoot)
	dir := filepath.Join(o.P.EngineDir, "zapret2-"+engine.Version)
	bin := filepath.Join(dir, "binaries", "linux-x86_64")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string]string{
		filepath.Join(bin, "nfqws2"):            "",
		filepath.Join(dir, "blockcheck2.sh"):    "#!/bin/sh\necho\necho '* SUMMARY'\n",
		filepath.Join(o.P.EngineDir, "current"): "zapret2-" + engine.Version + "\nlinux-x86_64\n",
	} {
		if err := os.WriteFile(name, []byte(data), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	oldIn, oldTTY := dnsPromptIn, dnsPromptTTY
	t.Cleanup(func() { dnsPromptIn, dnsPromptTTY = oldIn, oldTTY })
	dnsPromptIn, dnsPromptTTY = strings.NewReader("y\n"), func() bool { return true }

	out, _ := runOps(t, o, "scan", "--site", "pastebin.com", "--asn", "9121")
	has(t, out, "System DNS is already encrypted (dnscrypt-proxy), so the DNS poisoning check is skipped.")
	for _, not := range []string{"[y/N]", "DNS poisoning:", "--dns auto"} {
		if strings.Contains(out, not) {
			t.Errorf("output has %q:\n%s", not, out)
		}
	}
	if c, _ := config.Load(o.P); c.DNSOn() {
		t.Errorf("scan turned DNS on: %s", c.DNSProvider)
	}
}

func TestScanDNSChoice(t *testing.T) {
	poisoned := []probe.DNSResult{{
		Domain: "pastebin.com", Poisoned: true,
		System: []netip.Addr{netip.MustParseAddr("195.175.254.2")},
		DoH:    []netip.Addr{netip.MustParseAddr("104.20.0.1")},
	}}
	for _, tc := range []struct {
		name, flag, input string
		tty               bool
		want              string
		out, notOut       string
	}{
		{name: "tty yes", tty: true, input: "y\n", want: "auto", out: "keep it on? [y/N]"},
		{name: "tty default no", tty: true, input: "\n", out: "Keeping the system resolver"},
		{name: "no tty hints", out: "--dns auto", notOut: "[y/N]"},
		{name: "flag wins over tty", flag: "adguard", tty: true, want: "adguard", out: "Encrypted DNS (AdGuard)", notOut: "[y/N]"},
		{name: "flag keep", flag: "keep", tty: true, out: "Keeping the system resolver", notOut: "[y/N]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			oldIn, oldTTY := dnsPromptIn, dnsPromptTTY
			t.Cleanup(func() { dnsPromptIn, dnsPromptTTY = oldIn, oldTTY })
			dnsPromptIn = strings.NewReader(tc.input)
			dnsPromptTTY = func() bool { return tc.tty }
			var w bytes.Buffer
			got := scanDNSChoice(&ops.Ops{}, &w, tc.flag, poisoned)
			eq(t, got, tc.want)
			has(t, w.String(), "pastebin.com resolves to 195.175.254.2 here, but to 104.20.0.1")
			has(t, w.String(), tc.out)
			if tc.notOut != "" && strings.Contains(w.String(), tc.notOut) {
				t.Errorf("output has %q:\n%s", tc.notOut, w.String())
			}
		})
	}
}
