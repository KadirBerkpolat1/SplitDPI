// Package scan runs preset trials and blockcheck2.
package scan

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/KadirBerkpolat1/SplitDPI/internal/presets"
)

type BCLine struct {
	Test   string
	IPV    string
	Domain string
	Args   string // nfqws2 strategy tail (--… tokens only)
}

type BCCoverage struct {
	Count, Total int
	Args         string
}

type BlockcheckResult struct {
	Summary       []BCLine
	Coverage      []BCCoverage
	TLSCandidates []string // distinct valid TLS strategies, best first
	TCPTLS        string   // TLSCandidates[0]
	QUIC          string
}

type BlockcheckError struct {
	Tail []string
}

func (e BlockcheckError) Error() string {
	return strings.Join(e.Tail, "\n")
}

var (
	summaryLineRe  = regexp.MustCompile(`^(\S+)\s+(ipv[46])\s+(\S+)\s*:\s*(.+)$`)
	coverageLineRe = regexp.MustCompile(`^(\d+)/(\d+)\s*:\s*(\S+)\s+(ipv[46])\s*:\s*(.+)$`)
)

// ParseBlockcheck reads blockcheck2 log output. When * SUMMARY is missing it
// returns BlockcheckError with the last five non-empty lines.
func ParseBlockcheck(log string) (BlockcheckResult, error) {
	lines := strings.Split(log, "\n")
	var summary []BCLine
	var coverage []covRow
	hasSummary := false

	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		switch line {
		case "* SUMMARY":
			hasSummary = true
			for i++; i < len(lines); i++ {
				l := strings.TrimSpace(lines[i])
				if l == "" || strings.HasPrefix(l, "* ") {
					i--
					break
				}
				if bl, ok := parseSummaryLine(l); ok {
					summary = append(summary, bl)
				}
			}
		case "* COVERAGE":
			for i++; i < len(lines); i++ {
				l := strings.TrimSpace(lines[i])
				if l == "" || strings.HasPrefix(l, "* ") {
					i--
					break
				}
				if row, ok := parseCoverageLine(l); ok {
					coverage = append(coverage, row)
				}
			}
		}
	}

	if !hasSummary {
		return BlockcheckResult{}, BlockcheckError{Tail: lastNonEmpty(lines, 5)}
	}

	res := BlockcheckResult{Summary: summary}
	for _, c := range coverage {
		res.Coverage = append(res.Coverage, BCCoverage{Count: c.Count, Total: c.Total, Args: c.Args})
	}
	res.TLSCandidates = candidates(summary, coverage, "https")
	res.TCPTLS = first(res.TLSCandidates)
	res.QUIC = first(candidates(summary, coverage, "http3"))
	return res, nil
}

type covRow struct {
	Test         string
	Count, Total int
	Args         string
}

func parseSummaryLine(line string) (BCLine, bool) {
	m := summaryLineRe.FindStringSubmatch(line)
	if m == nil {
		return BCLine{}, false
	}
	args, ok := nfqws2Args(m[4])
	if !ok {
		args = ""
	}
	return BCLine{Test: m[1], IPV: m[2], Domain: m[3], Args: args}, true
}

func parseCoverageLine(line string) (covRow, bool) {
	m := coverageLineRe.FindStringSubmatch(line)
	if m == nil {
		return covRow{}, false
	}
	var count, total int
	if _, err := fmt.Sscanf(m[1]+"/"+m[2], "%d/%d", &count, &total); err != nil {
		return covRow{}, false
	}
	args, ok := nfqws2Args(m[5])
	if !ok {
		return covRow{}, false
	}
	return covRow{Test: m[3], Count: count, Total: total, Args: args}, true
}

func nfqws2Args(payload string) (string, bool) {
	payload = strings.TrimSpace(payload)
	const pref = "nfqws2 "
	if !strings.HasPrefix(payload, pref) {
		return "", false
	}
	rest := strings.TrimSpace(strings.TrimPrefix(payload, pref))
	if !strings.HasPrefix(rest, "--") {
		return "", false
	}
	return rest, true
}

// candidates lists the distinct valid strategies for the test, best first:
// COVERAGE order when present; else those working on both IPv4 and IPv6,
// then the rest in SUMMARY order. blockcheck2 tries simpler strategies first.
func candidates(summary []BCLine, coverage []covRow, testSubstr string) []string {
	var out []string
	add := func(args string) {
		if !slices.Contains(out, args) && presets.ValidateArgs(args) == nil {
			out = append(out, args)
		}
	}
	inSummary := map[string]bool{}
	var usable []BCLine
	on6 := map[string]bool{}
	for _, s := range summary {
		if strings.Contains(s.Test, testSubstr) && s.Args != "" {
			inSummary[s.Args] = true
			usable = append(usable, s)
			if s.IPV == "ipv6" {
				on6[s.Test+" "+s.Args] = true
			}
		}
	}

	if len(coverage) > 0 {
		for _, c := range coverage {
			if strings.Contains(c.Test, testSubstr) && inSummary[c.Args] {
				add(c.Args)
			}
		}
		return out
	}
	for _, s := range usable {
		if s.IPV == "ipv4" && on6[s.Test+" "+s.Args] {
			add(s.Args)
		}
	}
	for _, s := range usable {
		add(s.Args)
	}
	return out
}

func first(s []string) string {
	if len(s) == 0 {
		return ""
	}
	return s[0]
}

func lastNonEmpty(lines []string, n int) []string {
	var out []string
	for i := len(lines) - 1; i >= 0 && len(out) < n; i-- {
		if s := strings.TrimSpace(lines[i]); s != "" {
			out = append([]string{s}, out...)
		}
	}
	return out
}
