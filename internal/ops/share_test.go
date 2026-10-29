package ops

import (
	"net/url"
	"strings"
	"testing"

	"github.com/KadirBerkpolat1/SplitDPI/internal/config"
	"github.com/KadirBerkpolat1/SplitDPI/internal/presets"
	"github.com/KadirBerkpolat1/SplitDPI/internal/probe"
	"github.com/KadirBerkpolat1/SplitDPI/internal/scan"
)

func TestShareURL(t *testing.T) {
	rep := scan.Report{
		ASN: scan.ASNInfo{ASN: 9121, Country: "TR", Org: "Turk Telekom"},
		Trials: []scan.Trial{{
			Candidate: presets.Candidate{Strategy: presets.Strategy{ID: "tt-1"}, Source: "isp/tr/as9121-turk-telekom.toml"},
			Results: []probe.Result{
				{URL: "https://discord.com/", OK: true},
				{URL: "https://mysecret.example/", Err: "dial tcp 203.0.113.7:443: connection reset by peer"},
			},
		}},
		Winner: &config.Strategy{
			ID:     "tt-1",
			Source: "isp/tr/as9121-turk-telekom.toml",
			TCPTLS: "--lua-desync=multisplit:pos=1",
			QUIC:   "--lua-desync=fake:repeats=6",
			ASN:    9121,
			Date:   "2026-10-29",
			Engine: "v1.0.6",
		},
	}
	raw := ShareURL(rep, "v0.2.0")
	if !strings.HasPrefix(raw, ShareIssueURL+"?") {
		t.Fatalf("url %q", raw)
	}
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if got, want := q.Get("title"), "Working strategy: AS9121 Turk Telekom (TR)"; got != want {
		t.Errorf("title %q, want %q", got, want)
	}
	body := q.Get("body")
	for _, want := range []string{
		"ASN: AS9121\n", "ISP: Turk Telekom\n", "Country: TR\n",
		"Strategy: tt-1\n", "Source: isp/tr/as9121-turk-telekom.toml\n",
		"tcp_tls: --lua-desync=multisplit:pos=1\n", "quic: --lua-desync=fake:repeats=6\n",
		"zapret2: v1.0.6\n", "SplitDPI: v0.2.0\n", "Date: 2026-10-29\n",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body lacks %q:\n%s", want, body)
		}
	}
	for _, leak := range []string{"mysecret", "203.0.113.7", "discord.com"} {
		if strings.Contains(raw, leak) || strings.Contains(body, leak) {
			t.Errorf("share url leaks %q: %s", leak, raw)
		}
	}
}

func TestShareURLNoWinner(t *testing.T) {
	if got := ShareURL(scan.Report{ASN: scan.ASNInfo{ASN: 9121}}, "v0.2.0"); got != "" {
		t.Errorf("got %q", got)
	}
}
