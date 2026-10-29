package ops

import (
	"context"
	"maps"
	"slices"

	"github.com/KadirBerkpolat1/SplitDPI/internal/config"
	"github.com/KadirBerkpolat1/SplitDPI/internal/i18n"
	"github.com/KadirBerkpolat1/SplitDPI/internal/probe"
)

// NoAppsError means probe or scan was called with no enabled apps and no user sites.
type NoAppsError struct{}

func (NoAppsError) Error() string { return i18n.T("probe.no_apps") }

// Probe runs HTTPS probes for the given app ids and every user site. An empty
// slice uses config apps. Site results are keyed by the domain.
func (o *Ops) Probe(ctx context.Context, apps []string) (map[string][]probe.Result, error) {
	c, err := config.Load(o.P)
	if err != nil {
		return nil, err
	}
	sites, err := o.SitesList()
	if err != nil {
		return nil, err
	}
	if len(apps) == 0 {
		apps = c.Apps
	}
	if len(apps) == 0 && len(sites) == 0 {
		return nil, NoAppsError{}
	}
	set := o.PresetsList()
	out := make(map[string][]probe.Result)
	var urls []string
	type slot struct {
		app, url string
	}
	var order []slot
	for _, id := range apps {
		a, ok := set.Apps[id]
		if !ok {
			return nil, UnknownAppError{ID: id, Valid: slices.Sorted(maps.Keys(set.Apps))}
		}
		for _, u := range a.Probe {
			order = append(order, slot{id, u})
			urls = append(urls, u)
		}
	}
	for _, d := range sites {
		u := probe.SiteURL(d)
		order = append(order, slot{d, u})
		urls = append(urls, u)
	}
	p := probe.Prober{Client: o.HTTP, Timeout: probe.DefaultTimeout}
	results := p.Probe(ctx, urls)
	for i, s := range order {
		out[s.app] = append(out[s.app], results[i])
	}
	return out, nil
}
