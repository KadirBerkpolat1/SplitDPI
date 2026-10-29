package scan

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

type ASNInfo struct {
	ASN     int
	Country string
	Org     string
}

type ipinfoResp struct {
	Org     string `json:"org"`
	Country string `json:"country"`
}

// LookupASN fetches url and parses the ipinfo.io JSON shape (org = "AS<N> <name>").
func LookupASN(ctx context.Context, c *http.Client, url string) (ASNInfo, error) {
	if c == nil {
		c = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return ASNInfo{}, err
	}
	resp, err := c.Do(req)
	if err != nil {
		return ASNInfo{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusTooManyRequests {
		return ASNInfo{}, fmt.Errorf("HTTP 429")
	}
	if resp.StatusCode != http.StatusOK {
		return ASNInfo{}, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return ASNInfo{}, err
	}
	var raw ipinfoResp
	if err := json.Unmarshal(body, &raw); err != nil {
		return ASNInfo{}, err
	}
	return parseOrg(raw.Org, raw.Country)
}

func parseOrg(org, country string) (ASNInfo, error) {
	org = strings.TrimSpace(org)
	if org == "" {
		return ASNInfo{}, fmt.Errorf("missing org in response")
	}
	fields := strings.Fields(org)
	if len(fields) < 2 || !strings.HasPrefix(strings.ToUpper(fields[0]), "AS") {
		return ASNInfo{}, fmt.Errorf("invalid org %q", org)
	}
	n, err := strconv.Atoi(strings.TrimPrefix(strings.ToUpper(fields[0]), "AS"))
	if err != nil {
		return ASNInfo{}, fmt.Errorf("invalid org %q", org)
	}
	return ASNInfo{ASN: n, Country: strings.ToUpper(country), Org: strings.Join(fields[1:], " ")}, nil
}
