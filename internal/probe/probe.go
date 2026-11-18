// Package probe checks HTTPS reachability of app probe URLs.
package probe

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/KadirBerkpolat1/SplitDPI/internal/presets"
)

const DefaultTimeout = 8 * time.Second

type Result struct {
	URL     string
	OK      bool
	Err     string // the raw error text
	Cause   error  // the raw error, for Classify
	Elapsed time.Duration
	// NoAddress means the host has no address to test; see MarkNoAddress.
	NoAddress bool
}

type Prober struct {
	Client  *http.Client
	Timeout time.Duration
}

func (p Prober) timeout() time.Duration {
	if p.Timeout > 0 {
		return p.Timeout
	}
	return DefaultTimeout
}

func (p Prober) client() *http.Client {
	if p.Client != nil {
		return p.Client
	}
	return &http.Client{Timeout: p.timeout()}
}

// SiteURL is the probe URL of a user site's normalized domain.
func SiteURL(domain string) string { return "https://" + domain + "/" }

// AppURLs are the probe URLs of a. An app without any, like one the user
// made, is probed at each of its hosts.
func AppURLs(a presets.App) []string {
	if len(a.Probe) > 0 {
		return a.Probe
	}
	urls := make([]string, len(a.Hosts))
	for i, h := range a.Hosts {
		urls[i] = SiteURL(h)
	}
	return urls
}

// Hosts are the distinct hostnames of the probe URLs of apps, then sites.
// An app missing from set or a probe URL without a host is an error.
func Hosts(set presets.Set, apps, sites []string) ([]string, error) {
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
		for _, raw := range AppURLs(a) {
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

// maxParallel bounds the probes in flight: imported site lists can be long.
const maxParallel = 8

// Probe GETs each URL, up to maxParallel at once, so that blocked URLs cost
// one Timeout together rather than one each; the results keep the order of
// urls. OK means TLS verified and any HTTP status arrived within Timeout.
func (p Prober) Probe(ctx context.Context, urls []string) []Result {
	out := make([]Result, len(urls))
	sem := make(chan struct{}, maxParallel)
	var wg sync.WaitGroup
	for i, u := range urls {
		sem <- struct{}{}
		wg.Go(func() {
			defer func() { <-sem }()
			out[i] = p.probeOne(ctx, u)
		})
	}
	wg.Wait()
	return out
}

func (p Prober) probeOne(ctx context.Context, rawURL string) Result {
	start := time.Now()
	r := Result{URL: rawURL}
	to := p.timeout()
	reqCtx, cancel := context.WithTimeout(ctx, to)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, rawURL, nil)
	if err != nil {
		r.Err, r.Cause = err.Error(), err
		r.Elapsed = time.Since(start)
		return r
	}

	c := p.client()
	resp, err := c.Do(req)
	r.Elapsed = time.Since(start)
	if err != nil {
		r.Err, r.Cause = err.Error(), err
		return r
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	r.OK = true
	return r
}
