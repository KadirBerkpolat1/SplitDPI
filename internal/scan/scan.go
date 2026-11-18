package scan

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/KadirBerkpolat1/SplitDPI/internal/config"
	"github.com/KadirBerkpolat1/SplitDPI/internal/i18n"
	"github.com/KadirBerkpolat1/SplitDPI/internal/presets"
	"github.com/KadirBerkpolat1/SplitDPI/internal/probe"
	"github.com/KadirBerkpolat1/SplitDPI/internal/sysinfo"
)

// ErrNoStrategy means no preset trial and no blockcheck strategy succeeded.
var ErrNoStrategy = errors.New("no working strategy")

type Options struct {
	ASN            int
	Country        string
	Apps           []string
	Sites          []string // the user's normalized domains; trials must open them too
	Site           string   // one normalized domain: blockcheck2 tests only it, no preset trials
	BlockcheckOnly bool
	NoBlockcheck   bool
	// DNSCheck is asked before blockcheck2 runs, when DNS is off and some of
	// its domains look poisoned. It returns the dns_provider to turn on for
	// the rest of the scan and keep, or "" to stay on the system resolver; an
	// error, such as ctx's when the question was cancelled, ends the scan.
	DNSCheck func(ctx context.Context, poisoned []probe.DNSResult) (string, error)
	// DNSForce checks for poisoning even when the system already encrypts
	// DNS, where the check is otherwise skipped (an explicit --dns).
	DNSForce bool
	// Packages is asked before blockcheck2 runs when curl or nslookup/host
	// is missing and the package manager is known; true installs them. A
	// nil Packages never installs, and the scan stops there, as it does on
	// an error.
	Packages func(ctx context.Context, missing sysinfo.Packages) (bool, error)
	// Trial, when set, gets each finished trial, preset or blockcheck2, in
	// place of the progress line a blockcheck2 trial otherwise prints.
	Trial func(Trial)
}

type Env struct {
	Set    presets.Set
	Lookup func(context.Context) (ASNInfo, error)
	// StartTrial runs one candidate; country, the network's, picks the
	// exclude list as it will once the strategy is saved.
	StartTrial func(ctx context.Context, st presets.Strategy, country string) (stop func(), err error)
	// Probe tests urls through the trial running, each over a new connection.
	Probe      func(context.Context, []string) []probe.Result
	Blockcheck func(context.Context, BlockcheckParams, func(string)) (string, error)
	IPv6       bool
	Now        func() time.Time
	Progress   func(string)
	EngineVer  string
}

type Trial struct {
	Candidate presets.Candidate
	Results   []probe.Result
	OK        bool
}

type Report struct {
	ASN        ASNInfo
	Trials     []Trial
	Blockcheck *BlockcheckResult
	Winner     *config.Strategy
}

// Scan tries ISP preset candidates, then blockcheck2 unless disabled.
func Scan(ctx context.Context, env Env, opt Options) (Report, error) {
	if env.Now == nil {
		env.Now = time.Now
	}
	info, err := env.Lookup(ctx)
	if err != nil {
		return Report{}, err
	}
	rep := Report{ASN: info}
	if len(opt.Apps) == 0 && len(opt.Sites) == 0 && opt.Site == "" {
		return Report{}, fmt.Errorf("no apps")
	}
	if opt.Site != "" {
		opt.BlockcheckOnly = true
	}
	urls := probeURLs(env.Set, opt.Apps, opt.Sites)
	if len(urls) == 0 && opt.Site == "" {
		return Report{}, errors.New(i18n.T("probe.no_hosts"))
	}
	cands := env.Set.Candidates(info.ASN, info.Country)

	if !opt.BlockcheckOnly {
		for _, cand := range cands {
			stop, err := env.StartTrial(ctx, cand.Strategy, info.Country)
			if err != nil {
				return rep, err
			}
			results := env.Probe(ctx, urls)
			stop()
			if err := ctx.Err(); err != nil {
				return rep, err
			}
			ok := allProbeOK(results)
			rep.Trials = append(rep.Trials, Trial{Candidate: cand, Results: results, OK: ok})
			if opt.Trial != nil {
				opt.Trial(rep.Trials[len(rep.Trials)-1])
			}
			if ok {
				rep.Winner = strategyFromCandidate(cand, info, env.EngineVer, env.Now())
				return rep, nil
			}
		}
	}

	if err := ctx.Err(); err != nil {
		return rep, err
	}
	if opt.NoBlockcheck {
		return rep, ErrNoStrategy
	}

	hosts := []string{opt.Site}
	if opt.Site == "" {
		if hosts, err = probe.Hosts(env.Set, opt.Apps, opt.Sites); err != nil {
			return rep, err
		}
	}
	ipvs := "4"
	if env.IPv6 {
		ipvs = "46"
	}
	log, bcErr := env.Blockcheck(ctx, BlockcheckParams{
		Domains: hosts,
		IPVS:    ipvs,
		HTTP3:   anyQUIC(env.Set, opt.Apps),
	}, env.Progress)
	if bcErr != nil && log == "" {
		return rep, bcErr
	}
	bc, parseErr := ParseBlockcheck(log)
	if parseErr != nil {
		return rep, parseErr
	}
	rep.Blockcheck = &bc
	if bcErr != nil {
		return rep, bcErr
	}

	// A strategy that opens blockcheck2's domains can still break the apps and
	// the other sites, so each candidate gets the same trial as a preset.
	sites := opt.Sites
	if opt.Site != "" && !slices.Contains(sites, opt.Site) {
		sites = append(slices.Clone(sites), opt.Site)
	}
	urls = probeURLs(env.Set, opt.Apps, sites)
	tls := bc.TLSCandidates[:min(len(bc.TLSCandidates), MaxBlockcheckTrials)]
	if len(tls) > 0 && env.Progress != nil {
		env.Progress(i18n.N("scan.verify", len(tls)))
	}
	for i, args := range tls {
		st := presets.Strategy{ID: fmt.Sprintf("blockcheck-%d", i+1), TCPTLS: args, QUIC: bc.QUIC}
		stop, err := env.StartTrial(ctx, st, info.Country)
		if err != nil {
			return rep, err
		}
		results := env.Probe(ctx, urls)
		stop()
		if err := ctx.Err(); err != nil {
			return rep, err
		}
		ok := allProbeOK(results)
		rep.Trials = append(rep.Trials, Trial{Candidate: presets.Candidate{Strategy: st, Source: BlockcheckSource}, Results: results, OK: ok})
		if opt.Trial != nil {
			opt.Trial(rep.Trials[len(rep.Trials)-1])
		} else if env.Progress != nil {
			env.Progress(fmt.Sprintf("%s %s  %s", mark(ok), st.ID, args))
		}
		if ok {
			rep.Winner = &config.Strategy{
				ID:      "blockcheck",
				Source:  BlockcheckSource,
				TCPTLS:  args,
				QUIC:    bc.QUIC,
				ASN:     info.ASN,
				Country: info.Country,
				Date:    env.Now().Format("2006-01-02"),
				Engine:  env.EngineVer,
			}
			return rep, nil
		}
	}
	return rep, ErrNoStrategy
}

// BlockcheckSource is the Source of trials and winners that blockcheck2 found.
const BlockcheckSource = "blockcheck2"

// MaxBlockcheckTrials bounds how many blockcheck2 strategies are re-tested.
const MaxBlockcheckTrials = 20

func mark(ok bool) string {
	if ok {
		return "✓"
	}
	return "✗"
}

func strategyFromCandidate(c presets.Candidate, info ASNInfo, engineVer string, now time.Time) *config.Strategy {
	return &config.Strategy{
		ID:      c.Strategy.ID,
		Source:  c.Source,
		TCPTLS:  c.Strategy.TCPTLS,
		TCPHTTP: c.Strategy.TCPHTTP,
		QUIC:    c.Strategy.QUIC,
		ASN:     info.ASN,
		Country: info.Country,
		Date:    now.Format("2006-01-02"),
		Engine:  engineVer,
	}
}

// allProbeOK reports whether every result opened. One with no address to
// test (probe.MarkNoAddress) neither fails nor passes a trial alone.
func allProbeOK(rs []probe.Result) bool {
	tested := false
	for _, r := range rs {
		if r.NoAddress {
			continue
		}
		if !r.OK {
			return false
		}
		tested = true
	}
	return tested
}

// probeURLs are the distinct probe URLs of apps, then sites, in that order.
func probeURLs(set presets.Set, apps, sites []string) []string {
	var urls []string
	seen := map[string]bool{}
	add := func(u string) {
		if !seen[u] {
			seen[u] = true
			urls = append(urls, u)
		}
	}
	for _, id := range apps {
		if a, ok := set.Apps[id]; ok {
			for _, u := range probe.AppURLs(a) {
				add(u)
			}
		}
	}
	for _, d := range sites {
		add(probe.SiteURL(d))
	}
	return urls
}

func anyQUIC(set presets.Set, apps []string) bool {
	for _, id := range apps {
		if a, ok := set.Apps[id]; ok && a.QUIC {
			return true
		}
	}
	return false
}
