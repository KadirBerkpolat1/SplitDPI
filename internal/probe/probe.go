// Package probe checks HTTPS reachability of app probe URLs.
package probe

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
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

// AppURLs are the probe URLs of a whose host is one of a's hosts or under
// one: in apps mode the engine leaves any other host alone. An app without
// such a URL, like one the user made or one whose probed host the user
// removed, is probed at each of its hosts.
func AppURLs(a presets.App) []string {
	var urls []string
	for _, raw := range a.Probe {
		if u, err := url.Parse(raw); err == nil && covered(strings.ToLower(u.Hostname()), a.Hosts) {
			urls = append(urls, raw)
		}
	}
	if len(urls) > 0 {
		return urls
	}
	urls = make([]string, len(a.Hosts))
	for i, h := range a.Hosts {
		urls[i] = SiteURL(h)
	}
	return urls
}

// covered reports whether host is one of hosts or a subdomain of one.
func covered(host string, hosts []string) bool {
	for _, h := range hosts {
		if host == h || strings.HasSuffix(host, "."+h) {
			return true
		}
	}
	return false
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

// maxParallel bounds the probes and lookups in flight: imported site lists
// can be long.
const maxParallel = 8

// Probe GETs each URL, up to maxParallel at once, so that blocked URLs cost
// one Timeout together rather than one each; the results keep the order of
// urls. OK means TLS verified and any HTTP status arrived within Timeout.
// Once ctx is done no more probes start; those fail with ctx's error.
func (p Prober) Probe(ctx context.Context, urls []string) []Result {
	out := make([]Result, len(urls))
	ran := make([]bool, len(urls))
	each(ctx, len(urls), func(i int) {
		out[i], ran[i] = p.probeOne(ctx, urls[i]), true
	})
	for i := range out {
		if !ran[i] {
			out[i] = Result{URL: urls[i], Err: ctx.Err().Error(), Cause: ctx.Err()}
		}
	}
	return out
}

// each runs f(0) … f(n-1), up to maxParallel at once, and starts none once
// ctx is done.
func each(ctx context.Context, n int, f func(i int)) {
	sem := make(chan struct{}, maxParallel)
	var wg sync.WaitGroup
	for i := range n {
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
		}
		if ctx.Err() != nil {
			break
		}
		wg.Go(func() {
			defer func() { <-sem }()
			f(i)
		})
	}
	wg.Wait()
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
	if err != nil {
		r.Err, r.Cause = err.Error(), err
		r.Elapsed = time.Since(start)
		return r
	}
	// The status answered; a body, such as a stream that never ends, would
	// only hold the probe. Unread, the connection is not reused.
	resp.Body.Close()
	r.Elapsed = time.Since(start)
	r.OK = true
	return r
}
