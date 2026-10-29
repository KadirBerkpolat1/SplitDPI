package cli

import "testing"

func TestParseScanSite(t *testing.T) {
	opt, err := parseScanArgs([]string{"--site", "pastebin.com", "--asn", "9121"})
	if err != nil || opt.Site != "pastebin.com" || opt.ASN != 9121 {
		t.Fatalf("got %+v, %v", opt, err)
	}
	for _, args := range [][]string{{"--site"}, {"--site", "pastebin.com", "--no-blockcheck"}} {
		if _, err := parseScanArgs(args); err == nil {
			t.Errorf("%q accepted", args)
		}
	}
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
