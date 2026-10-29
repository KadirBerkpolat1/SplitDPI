// Package probe checks HTTPS reachability of app probe URLs.
package probe

import (
	"context"
	"io"
	"net/http"
	"time"
)

const DefaultTimeout = 8 * time.Second

type Result struct {
	URL     string
	OK      bool
	Err     string // the raw error text
	Cause   error  // the raw error, for Classify
	Elapsed time.Duration
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

// Probe GETs each URL. OK means TLS verified and any HTTP status arrived within Timeout.
func (p Prober) Probe(ctx context.Context, urls []string) []Result {
	out := make([]Result, len(urls))
	for i, u := range urls {
		out[i] = p.probeOne(ctx, u)
	}
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
