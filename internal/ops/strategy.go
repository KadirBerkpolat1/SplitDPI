package ops

import (
	"maps"
	"slices"

	"github.com/KadirBerkpolat1/SplitDPI/internal/config"
	"github.com/KadirBerkpolat1/SplitDPI/internal/engine"
	"github.com/KadirBerkpolat1/SplitDPI/internal/i18n"
	"github.com/KadirBerkpolat1/SplitDPI/internal/presets"
	"github.com/KadirBerkpolat1/SplitDPI/internal/supervisor"
)

// UnknownStrategyError is returned for a strategy id that no preset defines.
type UnknownStrategyError struct{ ID string }

func (e UnknownStrategyError) Error() string { return i18n.T("strategy.unknown", e.ID) }

// StrategyChoice is one preset strategy `strategy set` accepts.
type StrategyChoice struct {
	Strategy presets.Strategy
	Source   string // file within the preset tree, as scan records it
	ISP      string // ISP name; empty for country and generic strategies
	ASN      int    // the ISP's first ASN; 0 for country and generic strategies
	Country  string // country code; empty for generic strategies
}

// From names where the strategy comes from: the ISP, the country, or generic.
func (c StrategyChoice) From() string {
	switch {
	case c.ISP != "":
		return c.ISP
	case c.Country != "":
		return i18n.T("strategy.from_country", c.Country)
	}
	return i18n.T("strategy.from_generic")
}

// StrategyList returns every preset strategy and the configured strategy (nil if none).
func (o *Ops) StrategyList() ([]StrategyChoice, *config.Strategy, error) {
	c, err := config.Load(o.P)
	if err != nil {
		return nil, nil, err
	}
	return strategyChoices(o.PresetsList()), c.Strategy, nil
}

// strategyChoices lists ISP strategies, then country, then generic. A
// repeated id keeps its first occurrence, the one StrategySet picks.
func strategyChoices(set presets.Set) []StrategyChoice {
	var out []StrategyChoice
	add := func(ch StrategyChoice) {
		if !slices.ContainsFunc(out, func(x StrategyChoice) bool { return x.Strategy.ID == ch.Strategy.ID }) {
			out = append(out, ch)
		}
	}
	for _, isp := range set.ISPs {
		asn := 0
		if len(isp.ASN) > 0 {
			asn = isp.ASN[0]
		}
		for _, st := range isp.Strategies {
			add(StrategyChoice{Strategy: st, Source: isp.File, ISP: isp.Name, ASN: asn, Country: isp.Country})
		}
	}
	for _, code := range slices.Sorted(maps.Keys(set.Countries)) {
		for _, cand := range set.Candidates(0, code) {
			if cand.Source != presets.GenericFile {
				add(StrategyChoice{Strategy: cand.Strategy, Source: cand.Source, Country: code})
			}
		}
	}
	for _, st := range set.Generic {
		add(StrategyChoice{Strategy: st, Source: presets.GenericFile})
	}
	return out
}

// StrategySet saves the preset strategy id to config and returns it. It
// applies on the next start; running reports whether an instance is running now.
func (o *Ops) StrategySet(id string) (ch StrategyChoice, running bool, err error) {
	list := strategyChoices(o.PresetsList())
	i := slices.IndexFunc(list, func(ch StrategyChoice) bool { return ch.Strategy.ID == id })
	if i < 0 {
		return StrategyChoice{}, false, UnknownStrategyError{ID: id}
	}
	c, err := config.Load(o.P)
	if err != nil {
		return StrategyChoice{}, false, err
	}
	ch = list[i]
	st := &config.Strategy{
		ID:      ch.Strategy.ID,
		Source:  ch.Source,
		TCPTLS:  ch.Strategy.TCPTLS,
		TCPHTTP: ch.Strategy.TCPHTTP,
		QUIC:    ch.Strategy.QUIC,
		Date:    o.Now().Format("2006-01-02"),
		Engine:  engine.Version,
	}
	// ASN is the user's network as scan found it; it selects the country
	// exclude list. Without one, an ISP's strategy implies that ISP.
	if c.Strategy != nil {
		st.ASN = c.Strategy.ASN
	}
	if st.ASN == 0 {
		st.ASN = ch.ASN
	}
	c.Strategy = st
	if err := config.Save(o.P, c); err != nil {
		return StrategyChoice{}, false, err
	}
	_, _, held := supervisor.LockInfo(o.P)
	return ch, held, nil
}
