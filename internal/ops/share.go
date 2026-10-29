package ops

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/KadirBerkpolat1/SplitDPI/internal/scan"
)

// ShareIssueURL opens a new issue in the preset repository.
const ShareIssueURL = "https://github.com/KadirBerkpolat1/SplitDPI-presets/issues/new"

// ShareURL is a prefilled preset-repository issue reporting rep's winner, or
// "" without one. Opening it sends nothing until the user submits the issue.
// It carries only network and strategy facts: never IPs or the user's sites.
// The issue text stays English for the preset maintainers.
func ShareURL(rep scan.Report, version string) string {
	w := rep.Winner
	if w == nil {
		return ""
	}
	title := fmt.Sprintf("Working strategy: AS%d %s (%s)", rep.ASN.ASN, rep.ASN.Org, rep.ASN.Country)
	var b strings.Builder
	line := func(k, v string) {
		if v != "" {
			fmt.Fprintf(&b, "%s: %s\n", k, v)
		}
	}
	b.WriteString("```\n")
	line("ASN", fmt.Sprintf("AS%d", rep.ASN.ASN))
	line("ISP", rep.ASN.Org)
	line("Country", rep.ASN.Country)
	line("Strategy", w.ID)
	line("Source", w.Source)
	line("tcp_tls", w.TCPTLS)
	line("quic", w.QUIC)
	line("zapret2", w.Engine)
	line("SplitDPI", version)
	line("Date", w.Date)
	b.WriteString("```\n")
	q := url.Values{"title": {title}, "body": {b.String()}}
	return ShareIssueURL + "?" + q.Encode()
}
