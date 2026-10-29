package cli

import (
	"strings"
	"syscall"
	"testing"

	"github.com/KadirBerkpolat1/SplitDPI/internal/presets"
	"github.com/KadirBerkpolat1/SplitDPI/internal/probe"
	"github.com/KadirBerkpolat1/SplitDPI/internal/scan"
)

func TestParseScanSite(t *testing.T) {
	opt, verbose, err := parseScanArgs([]string{"--site", "pastebin.com", "--asn", "9121"})
	if err != nil || opt.Site != "pastebin.com" || opt.ASN != 9121 || verbose {
		t.Fatalf("got %+v, %v, %v", opt, verbose, err)
	}
	if _, verbose, err := parseScanArgs([]string{"-v", "--no-blockcheck"}); err != nil || !verbose {
		t.Errorf("-v: verbose %v, %v", verbose, err)
	}
	for _, args := range [][]string{{"--site"}, {"--site", "pastebin.com", "--no-blockcheck"}} {
		if _, _, err := parseScanArgs(args); err == nil {
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
