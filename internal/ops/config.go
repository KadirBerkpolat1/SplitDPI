package ops

import (
	"context"
	"errors"
	"net/url"
	"slices"
	"strconv"

	"github.com/KadirBerkpolat1/SplitDPI/internal/config"
	"github.com/KadirBerkpolat1/SplitDPI/internal/i18n"
	"github.com/KadirBerkpolat1/SplitDPI/internal/nft"
	"github.com/KadirBerkpolat1/SplitDPI/internal/presets"
	"github.com/KadirBerkpolat1/SplitDPI/internal/supervisor"
)

// ErrAppsModeEmpty refuses the apps mode when it would handle no traffic.
var ErrAppsModeEmpty = errors.New("apps mode needs at least one enabled app or site")

// SetMode saves the bypass scope: broad handles every site the autohostlist
// learns plus the user's sites, apps only the enabled apps and the user's
// sites. Like the other settings here, it applies on the next start.
func (o *Ops) SetMode(m config.Mode) error {
	if m != config.ModeBroad && m != config.ModeApps {
		return errors.New(i18n.T("config.err_mode", string(m)))
	}
	return o.updateConfig(func(c *config.Config) error {
		if m == config.ModeApps && len(c.Apps) == 0 {
			sites, err := config.ReadList(o.P.UserHosts)
			if err != nil {
				return err
			}
			if len(sites) == 0 {
				return ErrAppsModeEmpty
			}
		}
		c.Mode = m
		return nil
	})
}

// SetSkipIfaces saves the interfaces the bypass never touches. Names may end
// in the wildcard "*"; duplicates are dropped and the order kept.
func (o *Ops) SetSkipIfaces(list []string) error {
	// Never nil: a nil list is not saved, and Load would bring the defaults back.
	out := make([]string, 0, len(list))
	for _, i := range list {
		if !nft.ValidIface(i) {
			return errors.New(i18n.T("config.err_iface", i))
		}
		if !slices.Contains(out, i) {
			out = append(out, i)
		}
	}
	return o.updateConfig(func(c *config.Config) error {
		c.SkipIfaces = out
		return nil
	})
}

// SetIPInfoURL saves the service that finds the internet provider; "" is the default.
func (o *Ops) SetIPInfoURL(u string) error {
	if u == "" {
		u = config.Default().IPInfoURL
	}
	if p, err := url.Parse(u); err != nil || p.Scheme != "https" || p.Hostname() == "" {
		return errors.New(i18n.T("config.err_url", u))
	}
	return o.updateConfig(func(c *config.Config) error {
		c.IPInfoURL = u
		return nil
	})
}

// SetQNum saves the NFQUEUE number nfqws2 reads packets from.
func (o *Ops) SetQNum(n int) error {
	if n < 0 || n > 65535 {
		return errors.New(i18n.T("config.err_qnum", strconv.Itoa(n)))
	}
	return o.updateConfig(func(c *config.Config) error {
		c.QNum = n
		return nil
	})
}

// PresetsUpdate downloads the latest preset data and replaces the cache.
func (o *Ops) PresetsUpdate(ctx context.Context) (version string, apps int, err error) {
	set, err := presets.Update(ctx, o.HTTP, o.P, presets.UpdateURL)
	if err != nil {
		return "", 0, err
	}
	return set.Version, len(set.Apps), nil
}

// Running reports whether SplitDPI runs now, so a saved setting waits for a restart.
func (o *Ops) Running() bool {
	_, _, held := supervisor.LockInfo(o.P)
	return held
}

// updateConfig saves the config after f changes it; an error from f saves nothing.
func (o *Ops) updateConfig(f func(*config.Config) error) error {
	c, err := config.Load(o.P)
	if err != nil {
		return err
	}
	if err := f(&c); err != nil {
		return err
	}
	return config.Save(o.P, c)
}
