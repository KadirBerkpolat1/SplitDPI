package ops

import (
	"context"
	"fmt"
	"net/url"

	"github.com/KadirBerkpolat1/SplitDPI/internal/config"
	"github.com/KadirBerkpolat1/SplitDPI/internal/presets"
	"github.com/KadirBerkpolat1/SplitDPI/internal/scan"
)

// Detect resolves ASN/country (unless overridden) and lists preset candidates.
func (o *Ops) Detect(ctx context.Context, asn int, country string) (scan.ASNInfo, []presets.Candidate, error) {
	set := o.PresetsList()
	var info scan.ASNInfo
	switch {
	case asn != 0:
		info.ASN = asn
		if country != "" {
			info.Country = country
		}
		if isp := findISP(set, asn); isp != nil {
			if info.Country == "" {
				info.Country = isp.Country
			}
			info.Org = isp.Name
		} else if info.Country == "" {
			return scan.ASNInfo{}, nil, fmt.Errorf("unknown ASN %d", asn)
		}
	case country != "":
		info.Country = country
	default:
		c, err := config.Load(o.P)
		if err != nil {
			return scan.ASNInfo{}, nil, err
		}
		info, err = scan.LookupASN(ctx, o.HTTP, c.IPInfoURL)
		if err != nil {
			return scan.ASNInfo{}, nil, err
		}
	}
	return info, set.Candidates(info.ASN, info.Country), nil
}

// IPInfoHost returns the hostname from config's IP-info URL.
func (o *Ops) IPInfoHost() (string, error) {
	c, err := config.Load(o.P)
	if err != nil {
		return "", err
	}
	u, err := url.Parse(c.IPInfoURL)
	if err != nil || u.Hostname() == "" {
		return "", fmt.Errorf("invalid ipinfo_url")
	}
	return u.Hostname(), nil
}

func findISP(set presets.Set, asn int) *presets.ISP {
	for i := range set.ISPs {
		for _, a := range set.ISPs[i].ASN {
			if a == asn {
				return &set.ISPs[i]
			}
		}
	}
	return nil
}
