package nft_test

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/KadirBerkpolat1/SplitDPI/internal/nft"
)

var update = flag.Bool("update", false, "rewrite golden files")

func input() nft.RulesetInput {
	return nft.RulesetInput{
		Table:      "splitdpi",
		QNum:       200,
		TCPPorts:   []string{"443"},
		UDPPorts:   []string{"50000-50100", "19294-19344", "3478-3481", "5349", "1400"},
		SkipIfaces: []string{"wg*", "tun*", "tailscale*"},
	}
}

func ruleset(t *testing.T, in nft.RulesetInput) string {
	t.Helper()
	s, err := nft.Ruleset(in)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestRulesetGolden(t *testing.T) {
	got := ruleset(t, input())
	const golden = "testdata/default.nft"
	if *update {
		if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Errorf("ruleset differs from %s (rerun with -update to accept):\n%s", golden, got)
	}
}

func TestDNSRedirectGolden(t *testing.T) {
	in := input()
	in.DNSRedirectPort = 5453
	got := ruleset(t, in)
	const golden = "testdata/dns.nft"
	if *update {
		if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Errorf("ruleset differs from %s (rerun with -update to accept):\n%s", golden, got)
	}
	for _, s := range []string{"type nat hook output priority -100", "redirect to :5453", "ip daddr 127.0.0.0/8 return", "ip6 daddr ::1 return"} {
		if !strings.Contains(got, s) {
			t.Errorf("missing %q", s)
		}
	}
	if !strings.HasPrefix(got, "table inet splitdpi {") || strings.Count(got, "table ") != 1 {
		t.Error("the redirect must live inside our one table")
	}
}

// dnsChain returns the dns chain's rule lines, hook line excluded.
func dnsChain(t *testing.T, rs string) []string {
	t.Helper()
	_, body, ok := strings.Cut(rs, "\tchain dns {\n")
	if !ok {
		t.Fatalf("no dns chain:\n%s", rs)
	}
	body, _, _ = strings.Cut(body, "\t}\n")
	var out []string
	for _, l := range strings.Split(strings.TrimSpace(body), "\n")[1:] {
		out = append(out, strings.TrimSpace(l))
	}
	return out
}

// The forwarder's marked queries to local DNS servers must return before the
// redirect, or a local name would loop back into the forwarder forever.
func TestDNSMarkedQueriesNotRedirected(t *testing.T) {
	in := input()
	in.DNSRedirectPort = 5453
	rules := dnsChain(t, ruleset(t, in))
	if want := fmt.Sprintf("meta mark and 0x%08x != 0 return", nft.LocalDNSMark); rules[0] != want {
		t.Errorf("first dns rule %q, want %q", rules[0], want)
	}
	if nft.LocalDNSMark&(nft.DesyncMark|nft.PostNATMark) != 0 {
		t.Error("LocalDNSMark overlaps another mark")
	}
	if last := rules[len(rules)-1]; !strings.Contains(last, "redirect") {
		t.Errorf("last dns rule %q is not the redirect", last)
	}
	if n := strings.Count(strings.Join(rules, "\n"), "redirect"); n != 1 {
		t.Errorf("%d redirect rules", n)
	}
}

// VPN interfaces keep their own DNS (split DNS): the dns chain skips the same
// interfaces as the queue chains, before the redirect.
func TestDNSSkipsIfaces(t *testing.T) {
	in := input()
	in.DNSRedirectPort = 5453
	in.SkipIfaces = []string{"wg*", "corp0"}
	rules := dnsChain(t, ruleset(t, in))
	want := []string{
		"meta mark and 0x10000000 != 0 return",
		`oifname "lo" return`,
		`oifname "wg*" return`,
		`oifname "corp0" return`,
		"ip daddr 127.0.0.0/8 return",
		"ip6 daddr ::1 return",
		"meta l4proto { tcp, udp } th dport 53 counter redirect to :5453",
	}
	if strings.Join(rules, "\n") != strings.Join(want, "\n") {
		t.Errorf("dns chain:\n%s\nwant:\n%s", strings.Join(rules, "\n"), strings.Join(want, "\n"))
	}
}

func TestNoDNSRedirectByDefault(t *testing.T) {
	if s := ruleset(t, input()); strings.Contains(s, "redirect") || strings.Contains(s, "type nat") {
		t.Errorf("redirect without DNSRedirectPort:\n%s", s)
	}
}

func TestNeverFlushesRuleset(t *testing.T) {
	s := ruleset(t, input())
	if strings.Contains(s, "flush ruleset") {
		t.Error("ruleset flushes the whole ruleset")
	}
	if !strings.HasPrefix(s, "table inet splitdpi {") {
		t.Errorf("ruleset does not start with our table:\n%s", s)
	}
}

func TestBypassEverywhere(t *testing.T) {
	n := 0
	for _, l := range strings.Split(ruleset(t, input()), "\n") {
		if strings.Contains(l, "queue") {
			n++
			if !strings.Contains(l, "bypass") || !strings.Contains(l, "counter") {
				t.Errorf("queue rule without bypass/counter: %q", l)
			}
		}
	}
	if n != 4 {
		t.Errorf("%d queue rules, want 4 (tcp/udp out, tcp/udp reply)", n)
	}
}

func TestReplyQueued(t *testing.T) {
	s := ruleset(t, input())
	for _, want := range []string{"tcp sport { 443 } ct reply packets 1-10", "ct original packets 1-20", "ct original packets 1-5", "ct reply packets 1-3"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q", want)
		}
	}
}

func TestSkipsLoAndIfaces(t *testing.T) {
	s := ruleset(t, input())
	for _, want := range []string{`oifname "lo" return`, `iifname "lo" return`, `oifname "wg*" return`, `iifname "tailscale*" return`} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q", want)
		}
	}
}

func TestNoUDPPorts(t *testing.T) {
	in := input()
	in.UDPPorts = nil
	if s := ruleset(t, in); strings.Contains(s, "udp") {
		t.Errorf("udp rule without UDP ports:\n%s", s)
	}
}

func TestRulesetRejectsInjection(t *testing.T) {
	for name, mod := range map[string]func(*nft.RulesetInput){
		"iface quote": func(in *nft.RulesetInput) { in.SkipIfaces = []string{`wg*" return; flush ruleset; "`} },
		"iface long":  func(in *nft.RulesetInput) { in.SkipIfaces = []string{"abcdefghijklmnopq"} },
		"port":        func(in *nft.RulesetInput) { in.UDPPorts = []string{"53 } accept"} },
		"qnum":        func(in *nft.RulesetInput) { in.QNum = 70000 },
		"dns port":    func(in *nft.RulesetInput) { in.DNSRedirectPort = 70000 },
		"table":       func(in *nft.RulesetInput) { in.Table = "x; flush ruleset" },
	} {
		in := input()
		mod(&in)
		if _, err := nft.Ruleset(in); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

type call struct{ cmd, stdin string }

type fake struct {
	calls []call
	fail  map[string]string // command line -> error text
	out   map[string]string
}

func (f *fake) run(_ context.Context, stdin, name string, args ...string) ([]byte, error) {
	cmd := strings.Join(append([]string{name}, args...), " ")
	f.calls = append(f.calls, call{cmd, stdin})
	if msg, ok := f.fail[cmd]; ok {
		return nil, errors.New(msg)
	}
	return []byte(f.out[cmd]), nil
}

const noTable = "nft: exit status 1: Error: No such file or directory; did you mean table 'zapret2' in family inet?"

func TestApplySequence(t *testing.T) {
	f := &fake{}
	rs := ruleset(t, input())
	if err := nft.Apply(context.Background(), f.run, "splitdpi", rs); err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 2 || f.calls[0].cmd != "nft delete table inet splitdpi" ||
		f.calls[1].cmd != "nft -f -" || f.calls[1].stdin != rs {
		t.Errorf("calls %+v", f.calls)
	}
}

func TestApplyFreshTable(t *testing.T) {
	f := &fake{fail: map[string]string{"nft delete table inet splitdpi": noTable}}
	if err := nft.Apply(context.Background(), f.run, "splitdpi", ruleset(t, input())); err != nil {
		t.Fatal(err)
	}
}

func TestApplyLoadFails(t *testing.T) {
	f := &fake{fail: map[string]string{"nft -f -": "nft: exit status 1: syntax error"}}
	if err := nft.Apply(context.Background(), f.run, "splitdpi", "x"); err == nil {
		t.Error("load failure not reported")
	}
}

func TestDeleteMissingOK(t *testing.T) {
	f := &fake{fail: map[string]string{"nft delete table inet splitdpi": noTable}}
	if err := nft.Delete(context.Background(), f.run, "splitdpi"); err != nil {
		t.Errorf("got %v", err)
	}
	f = &fake{fail: map[string]string{"nft delete table inet splitdpi": "nft: exit status 1: Operation not permitted"}}
	if err := nft.Delete(context.Background(), f.run, "splitdpi"); err == nil {
		t.Error("real failure swallowed")
	}
}

func TestDeleteChain(t *testing.T) {
	f := &fake{}
	if err := nft.DeleteChain(context.Background(), f.run, "splitdpi", nft.DNSChain); err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 1 || f.calls[0].cmd != "nft delete chain inet splitdpi dns" {
		t.Errorf("calls %+v", f.calls)
	}
	f = &fake{fail: map[string]string{"nft delete chain inet splitdpi dns": noTable}}
	if err := nft.DeleteChain(context.Background(), f.run, "splitdpi", nft.DNSChain); err != nil {
		t.Errorf("missing chain: %v", err)
	}
}

func TestExists(t *testing.T) {
	f := &fake{}
	if ok, err := nft.Exists(context.Background(), f.run, "splitdpi"); !ok || err != nil {
		t.Errorf("present: %v %v", ok, err)
	}
	f = &fake{fail: map[string]string{"nft list table inet splitdpi": noTable}}
	if ok, err := nft.Exists(context.Background(), f.run, "splitdpi"); ok || err != nil {
		t.Errorf("absent: %v %v", ok, err)
	}
}
