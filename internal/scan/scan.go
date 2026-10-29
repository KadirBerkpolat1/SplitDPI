package scan

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"time"

	"github.com/KadirBerkpolat1/SplitDPI/internal/config"
	"github.com/KadirBerkpolat1/SplitDPI/internal/i18n"
	"github.com/KadirBerkpolat1/SplitDPI/internal/presets"
	"github.com/KadirBerkpolat1/SplitDPI/internal/probe"
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
}

type Env struct {
	Set          presets.Set
	Lookup       func(context.Context) (ASNInfo, error)
	StartTrial   func(context.Context, presets.Strategy) (stop func(), err error)
	Probe        func(context.Context, []string) []probe.Result
	Blockcheck   func(context.Context, BlockcheckParams, func(string)) (string, error)
	IPv6         bool
	Now          func() time.Time
	Progress     func(string)
	EngineVer    string
}

type Trial struct {
	Candidate presets.Candidate
	Results   []probe.Result
	OK        bool
}

type Report struct {
	ASN                  ASNInfo
	Trials               []Trial
	Blockcheck           *BlockcheckResult
	Winner               *config.Strategy
	VoiceUnverified      bool
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
	rep.VoiceUnverified = voiceUnverified(env.Set, opt.Apps)
	urls := probeURLs(env.Set, opt.Apps, opt.Sites)
	cands := env.Set.Candidates(info.ASN, info.Country)

	if !opt.BlockcheckOnly {
		for _, cand := range cands {
			stop, err := env.StartTrial(ctx, cand.Strategy)
			if err != nil {
				return rep, err
			}
			results := env.Probe(ctx, urls)
			stop()
			ok := allProbeOK(results)
			rep.Trials = append(rep.Trials, Trial{Candidate: cand, Results: results, OK: ok})
			if ok {
				rep.Winner = strategyFromCandidate(cand, info, env.EngineVer, env.Now())
				return rep, nil
			}
		}
	}

	if opt.NoBlockcheck {
		return rep, ErrNoStrategy
	}

	hosts := []string{opt.Site}
	if opt.Site == "" {
		if hosts, err = probeHosts(env.Set, opt.Apps, opt.Sites); err != nil {
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
		env.Progress(i18n.T("scan.verify", len(tls)))
	}
	for i, args := range tls {
		st := presets.Strategy{ID: fmt.Sprintf("blockcheck-%d", i+1), TCPTLS: args, QUIC: bc.QUIC}
		stop, err := env.StartTrial(ctx, st)
		if err != nil {
			return rep, err
		}
		results := env.Probe(ctx, urls)
		stop()
		ok := allProbeOK(results)
		rep.Trials = append(rep.Trials, Trial{Candidate: presets.Candidate{Strategy: st, Source: BlockcheckSource}, Results: results, OK: ok})
		if env.Progress != nil {
			env.Progress(fmt.Sprintf("%s %s  %s", mark(ok), st.ID, args))
		}
		if ok {
			rep.Winner = &config.Strategy{
				ID:     "blockcheck",
				Source: BlockcheckSource,
				TCPTLS: args,
				QUIC:   bc.QUIC,
				ASN:    info.ASN,
				Date:   env.Now().Format("2006-01-02"),
				Engine: env.EngineVer,
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
		Date:    now.Format("2006-01-02"),
		Engine:  engineVer,
	}
}

func allProbeOK(rs []probe.Result) bool {
	for _, r := range rs {
		if !r.OK {
			return false
		}
	}
	return len(rs) > 0
}

func probeURLs(set presets.Set, apps, sites []string) []string {
	var urls []string
	for _, id := range apps {
		if a, ok := set.Apps[id]; ok {
			urls = append(urls, a.Probe...)
		}
	}
	for _, d := range sites {
		urls = append(urls, probe.SiteURL(d))
	}
	return urls
}

func probeHosts(set presets.Set, apps, sites []string) ([]string, error) {
	seen := map[string]bool{}
	var hosts []string
	add := func(h string) {
		if !seen[h] {
			seen[h] = true
			hosts = append(hosts, h)
		}
	}
	for _, id := range apps {
		a, ok := set.Apps[id]
		if !ok {
			return nil, fmt.Errorf("unknown app %q", id)
		}
		for _, raw := range a.Probe {
			u, err := url.Parse(raw)
			if err != nil || u.Hostname() == "" {
				return nil, fmt.Errorf("bad probe url %q", raw)
			}
			add(u.Hostname())
		}
	}
	for _, d := range sites {
		add(d)
	}
	return hosts, nil
}

func anyQUIC(set presets.Set, apps []string) bool {
	for _, id := range apps {
		if a, ok := set.Apps[id]; ok && a.QUIC {
			return true
		}
	}
	return false
}

func voiceUnverified(set presets.Set, apps []string) bool {
	for _, id := range apps {
		if id != "discord" {
			continue
		}
		a, ok := set.Apps[id]
		return ok && a.UDP != nil
	}
	return false
}
