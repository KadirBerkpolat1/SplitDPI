package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/KadirBerkpolat1/SplitDPI/internal/dns"
	"github.com/KadirBerkpolat1/SplitDPI/internal/domain"
	"github.com/KadirBerkpolat1/SplitDPI/internal/i18n"
	"github.com/KadirBerkpolat1/SplitDPI/internal/ops"
	"github.com/KadirBerkpolat1/SplitDPI/internal/probe"
	"github.com/KadirBerkpolat1/SplitDPI/internal/scan"
)

func init() {
	commands["scan"] = Command{Name: "scan", Root: true, Help: "cmd.scan.help", Run: runScan}
}

func runScan(ctx context.Context, o *ops.Ops, w io.Writer, args []string) error {
	opt, verbose, dnsFlag, err := parseScanArgs(args)
	if err != nil {
		return usageError(i18n.T("scan.usage"))
	}
	opt.DNSCheck = func(_ context.Context, poisoned []probe.DNSResult) string {
		return scanDNSChoice(o, w, dnsFlag, poisoned)
	}
	if opt.Site != "" {
		if opt.Site, err = domain.Normalize(opt.Site); err != nil {
			return err
		}
		fmt.Fprintln(w, siteScanText(opt.Site, o.HasIPv6(ctx)))
	}
	progress := func(line string) { fmt.Fprintln(w, line) }
	rep, err := o.Scan(ctx, opt, progress)
	if err != nil {
		var lock ops.RunLockError
		if errors.As(err, &lock) {
			return err
		}
		var bc scan.BlockcheckError
		if errors.As(err, &bc) {
			for _, ln := range bc.Tail {
				fmt.Fprintln(w, ln)
			}
		}
		if errors.Is(err, scan.ErrNoStrategy) {
			printTrials(w, rep.Trials, verbose)
			return errors.New(i18n.T("scan.no_strategy"))
		}
		return err
	}
	printTrials(w, rep.Trials, verbose)
	if rep.Winner != nil {
		fmt.Fprintln(w, i18n.T("scan.winner", rep.Winner.ID, rep.Winner.Source))
		if opt.Site != "" {
			fmt.Fprintln(w, i18n.T("sites.added", opt.Site))
		}
		fmt.Fprintln(w, i18n.T("share.scan", ops.ShareURL(rep, Version)))
	}
	if rep.VoiceUnverified {
		fmt.Fprintln(w, i18n.T("scan.voice"))
	}
	suggestPresetsUpdate(w, o)
	fmt.Fprintln(w, i18n.T("scan.next"))
	return nil
}

// printTrials lists each trial and, for a failed one, why each probe failed.
func printTrials(w io.Writer, trials []scan.Trial, verbose bool) {
	for _, tr := range trials {
		mark := "✗"
		if tr.OK {
			mark = "✓"
		}
		line := fmt.Sprintf("%s %s (%s)", mark, tr.Candidate.Strategy.ID, tr.Candidate.Source)
		if tr.Candidate.Source == scan.BlockcheckSource {
			line += "  " + tr.Candidate.Strategy.TCPTLS
		}
		fmt.Fprintln(w, line)
		for _, r := range tr.Results {
			if !r.OK {
				fmt.Fprintf(w, "    %s  %s\n", r.URL, probe.Classify(r).What())
				printRawErr(w, r, verbose)
			}
		}
	}
}

// siteScanText says which IP versions a site scan tests.
func siteScanText(site string, ipv6 bool) string {
	if ipv6 {
		return i18n.T("scan.site_ipv46", site)
	}
	return i18n.T("scan.site_ipv4", site)
}

func suggestPresetsUpdate(w io.Writer, o *ops.Ops) {
	set := o.PresetsList()
	if set.Version == "" {
		return
	}
	t, err := time.Parse("2006-01-02", set.Version)
	if err != nil {
		return
	}
	if time.Since(t) > 30*24*time.Hour {
		fmt.Fprintln(w, i18n.T("scan.presets_stale"))
	}
}

// Before blockcheck2 tests poisoned domains, the scan asks a terminal user
// whether to turn on encrypted DNS; these are the prompt's input.
var (
	dnsPromptIn  io.Reader = os.Stdin
	dnsPromptTTY           = func() bool { return term.IsTerminal(int(os.Stdin.Fd())) }
)

// scanDNSChoice answers ops' DNS question: dnsFlag is --dns ("" when not given).
func scanDNSChoice(o *ops.Ops, w io.Writer, dnsFlag string, poisoned []probe.DNSResult) string {
	for _, r := range poisoned {
		fmt.Fprintln(w, i18n.T("dns2.scan_poisoned", r.Domain, joinAddrs(r.System), joinAddrs(r.DoH)))
	}
	choice := dnsFlag
	switch {
	case dnsFlag != "":
	case dnsPromptTTY():
		fmt.Fprint(w, i18n.T("dns2.scan_ask")+" ")
		line, _ := bufio.NewReader(dnsPromptIn).ReadString('\n')
		if slices.Contains(strings.Split(i18n.T("dns2.yes"), ","), strings.ToLower(strings.TrimSpace(line))) {
			choice = dns.Auto
		}
	default:
		fmt.Fprintln(w, i18n.T("dns2.scan_hint"))
	}
	if choice == "" || choice == "keep" {
		fmt.Fprintln(w, i18n.T("dns2.scan_kept"))
		return ""
	}
	fmt.Fprintln(w, i18n.T("dns2.scan_on", dnsChoice(o, choice).Name))
	return choice
}

func joinAddrs(addrs []netip.Addr) string {
	s := make([]string, len(addrs))
	for i, a := range addrs {
		s[i] = a.String()
	}
	return strings.Join(s, ", ")
}

// parseScanArgs also returns -v and --dns: "keep" or a provider, "" when not given.
func parseScanArgs(args []string) (opt scan.Options, verbose bool, dnsFlag string, err error) {
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-v", "--verbose":
			verbose = true
		case "--dns":
			if i+1 >= len(args) {
				return opt, false, "", fmt.Errorf("missing")
			}
			dnsFlag = args[i+1]
			if dnsFlag != "keep" && (!dns.Valid(dnsFlag) || dnsFlag == dns.Off) {
				return opt, false, "", fmt.Errorf("bad dns")
			}
			i++
		case "--asn":
			if i+1 >= len(args) {
				return opt, false, "", fmt.Errorf("missing")
			}
			n, err := strconv.Atoi(args[i+1])
			if err != nil || n <= 0 {
				return opt, false, "", fmt.Errorf("bad asn")
			}
			opt.ASN = n
			i++
		case "--country":
			if i+1 >= len(args) {
				return opt, false, "", fmt.Errorf("missing")
			}
			opt.Country = strings.ToUpper(args[i+1])
			i++
		case "--apps":
			if i+1 >= len(args) {
				return opt, false, "", fmt.Errorf("missing")
			}
			for _, a := range strings.Split(args[i+1], ",") {
				a = strings.TrimSpace(a)
				if a != "" {
					opt.Apps = append(opt.Apps, a)
				}
			}
			i++
		case "--site":
			if i+1 >= len(args) {
				return opt, false, "", fmt.Errorf("missing")
			}
			opt.Site = args[i+1]
			i++
		case "--blockcheck-only":
			opt.BlockcheckOnly = true
		case "--no-blockcheck":
			opt.NoBlockcheck = true
		default:
			return opt, false, "", fmt.Errorf("unknown")
		}
	}
	if opt.Site != "" && opt.NoBlockcheck {
		return opt, false, "", fmt.Errorf("--site needs blockcheck2")
	}
	return opt, verbose, dnsFlag, nil
}
