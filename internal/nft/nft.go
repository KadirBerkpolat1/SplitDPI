// Package nft renders and manages splitdpi's private nftables table.
// It never touches other tables and never flushes the ruleset.
package nft

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/KadirBerkpolat1/SplitDPI/internal/execx"
)

// Values from upstream config.default:58-59, 74-77.
const (
	DesyncMark    = 0x40000000 // nfqws2's default --fwmark: its own packets
	PostNATMark   = 0x20000000
	packetsOutTCP = 20
	packetsOutUDP = 5
	packetsInTCP  = 10
	packetsInUDP  = 3
)

// DNSChain holds the DNS redirect. It is the one chain that would block
// traffic without us (its forwarder lives in the supervisor), so it can be
// dropped on its own while the fail-open queue chains stay.
const DNSChain = "dns"

type RulesetInput struct {
	Table              string
	QNum               int
	TCPPorts, UDPPorts []string
	SkipIfaces         []string // nft wildcards allowed, e.g. "wg*"
	DNSRedirectPort    int      // 0 = off; else outbound port 53 goes to this local port
}

var (
	tableRe = regexp.MustCompile(`^[a-z0-9_]+$`)
	ifaceRe = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,15}\*?$`)
	portRe  = regexp.MustCompile(`^[0-9]{1,5}(-[0-9]{1,5})?$`)
)

// Ruleset renders the table with upstream's post-NAT shapes (common/nft.sh):
// outgoing first packets queued at postrouting 101, replies at prerouting -101
// (autohostlist needs them), and nfqws2's own packets notracked at output -401.
// The input is checked because SkipIfaces comes from a hand-edited config.
func Ruleset(in RulesetInput) (string, error) {
	if !tableRe.MatchString(in.Table) {
		return "", fmt.Errorf("invalid nft table name %q", in.Table)
	}
	if in.QNum < 0 || in.QNum > 65535 {
		return "", fmt.Errorf("queue number %d is outside 0-65535", in.QNum)
	}
	if in.DNSRedirectPort < 0 || in.DNSRedirectPort > 65535 {
		return "", fmt.Errorf("DNS redirect port %d is outside 0-65535", in.DNSRedirectPort)
	}
	for _, i := range in.SkipIfaces {
		if !ifaceRe.MatchString(i) {
			return "", fmt.Errorf("invalid interface name %q", i)
		}
	}
	for _, p := range append(append([]string{}, in.TCPPorts...), in.UDPPorts...) {
		if !portRe.MatchString(p) {
			return "", fmt.Errorf("invalid port %q", p)
		}
	}
	skips := append([]string{"lo"}, in.SkipIfaces...)
	queue := fmt.Sprintf("counter queue num %d bypass", in.QNum)
	notOurs := fmt.Sprintf("meta mark and 0x%08x == 0", DesyncMark)

	var b strings.Builder
	fmt.Fprintf(&b, "table inet %s {\n", in.Table)

	b.WriteString("\tchain postnat {\n\t\ttype filter hook postrouting priority 101; policy accept;\n")
	for _, i := range skips {
		fmt.Fprintf(&b, "\t\toifname %q return\n", i)
	}
	setMark := fmt.Sprintf("meta mark set meta mark or 0x%08x", PostNATMark)
	for _, r := range []struct {
		proto string
		ports []string
		n     int
	}{{"tcp", in.TCPPorts, packetsOutTCP}, {"udp", in.UDPPorts, packetsOutUDP}} {
		if len(r.ports) > 0 {
			fmt.Fprintf(&b, "\t\t%s %s dport { %s } ct original packets 1-%d %s %s\n", notOurs, r.proto, strings.Join(r.ports, ", "), r.n, setMark, queue)
		}
	}
	b.WriteString("\t}\n")

	b.WriteString("\tchain prenat {\n\t\ttype filter hook prerouting priority -101; policy accept;\n")
	for _, i := range skips {
		fmt.Fprintf(&b, "\t\tiifname %q return\n", i)
	}
	for _, r := range []struct {
		proto string
		ports []string
		n     int
	}{{"tcp", in.TCPPorts, packetsInTCP}, {"udp", in.UDPPorts, packetsInUDP}} {
		if len(r.ports) > 0 {
			fmt.Fprintf(&b, "\t\t%s %s sport { %s } ct reply packets 1-%d %s\n", notOurs, r.proto, strings.Join(r.ports, ", "), r.n, queue)
		}
	}
	b.WriteString("\t}\n")

	// Port 53 goes to our local DoH forwarder; loopback stays, so
	// local stub resolvers are reached and their upstream queries redirected.
	if in.DNSRedirectPort > 0 {
		fmt.Fprintf(&b, `	chain %s {
		type nat hook output priority -100; policy accept;
		ip daddr 127.0.0.0/8 return
		ip6 daddr ::1 return
		meta l4proto { tcp, udp } th dport 53 counter redirect to :%d
	}
`, DNSChain, in.DNSRedirectPort)
	}

	fmt.Fprintf(&b, `	chain predefrag {
		type filter hook output priority -401; policy accept;
		meta mark and 0x%08x != 0 jump predefrag_nfqws comment "nfqws generated : avoid drop by INVALID conntrack state"
	}
	chain predefrag_nfqws {
		meta mark and 0x%08x != 0 notrack comment "postnat traffic"
		ip frag-off & 0x1fff != 0 notrack comment "ipfrag"
		exthdr frag exists notrack comment "ipfrag"
		tcp flags ! syn,rst,ack notrack comment "datanoack"
	}
}
`, DesyncMark, PostNATMark)
	return b.String(), nil
}

// Apply replaces our table: delete it if present, then load the ruleset.
func Apply(ctx context.Context, run execx.Runner, table, ruleset string) error {
	if err := Delete(ctx, run, table); err != nil {
		return err
	}
	if _, err := run(ctx, ruleset, "nft", "-f", "-"); err != nil {
		return fmt.Errorf("load nft table %s: %w", table, err)
	}
	return nil
}

// Delete removes our table. A missing table is not an error.
func Delete(ctx context.Context, run execx.Runner, table string) error {
	if _, err := run(ctx, "", "nft", "delete", "table", "inet", table); err != nil && !missing(err) {
		return fmt.Errorf("delete nft table %s: %w", table, err)
	}
	return nil
}

// DeleteChain removes one chain of our table, rules included. A missing chain
// or table is not an error.
func DeleteChain(ctx context.Context, run execx.Runner, table, chain string) error {
	if _, err := run(ctx, "", "nft", "delete", "chain", "inet", table, chain); err != nil && !missing(err) {
		return fmt.Errorf("delete nft chain %s %s: %w", table, chain, err)
	}
	return nil
}

func Exists(ctx context.Context, run execx.Runner, table string) (bool, error) {
	_, err := run(ctx, "", "nft", "list", "table", "inet", table)
	switch {
	case err == nil:
		return true, nil
	case missing(err):
		return false, nil
	}
	return false, err
}

func missing(err error) bool {
	return strings.Contains(err.Error(), "No such file or directory")
}
