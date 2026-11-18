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

// ErrAppsModeEmpty refuses a change that leaves apps mode with no domain to
// handle: no enabled app with an address and no site of the user's. No run
// could start then (render.ErrNoApps).
var ErrAppsModeEmpty error = appsModeEmptyError{}

type appsModeEmptyError struct{}

func (appsModeEmptyError) Error() string { return i18n.T("config.apps_empty") }

// checkAppsMode returns ErrAppsModeEmpty when c, in apps mode, would handle
// no domain. changed, when its ID is set, is that app as it will be;
// removing is a site about to be removed.
func (o *Ops) checkAppsMode(c config.Config, changed presets.App, removing string) error {
	if c.Mode != config.ModeApps {
		return nil
	}
	set := o.PresetsList()
	for _, id := range c.Apps {
		a := set.Apps[id]
		if id == changed.ID {
			a = changed
		}
		if len(a.Hosts) > 0 {
			return nil
		}
	}
	sites, err := config.ReadList(o.P.UserHosts)
	if err != nil {
		return err
	}
	if slices.ContainsFunc(sites, func(d string) bool { return d != removing }) {
		return nil
	}
	return ErrAppsModeEmpty
}

// SetMode saves the bypass scope: broad handles every site the autohostlist
// learns plus the user's sites, apps only the enabled apps and the user's
// sites. Like the other settings here, it applies on the next start.
func (o *Ops) SetMode(m config.Mode) error {
	if m != config.ModeBroad && m != config.ModeApps {
		return errors.New(i18n.T("config.err_mode", string(m)))
	}
	return o.updateConfig(func(c *config.Config) error {
		c.Mode = m
		return o.checkAppsMode(*c, presets.App{}, "")
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
	ctx, cancel := context.WithTimeout(ctx, presetsTimeout)
	defer cancel()
	set, err := presets.Update(ctx, o.HTTP, o.P, presets.UpdateURL)
	if err != nil {
		return "", 0, err
	}
	return set.Version, len(set.Apps), nil
}

// PresetsUpdateError words a checksum that failed the presets release: why
// nothing was changed and what to do. Other errors stay as they are.
func PresetsUpdateError(err error) error {
	switch {
	case errors.Is(err, presets.ErrNoChecksum):
		return errors.New(i18n.T("presets.update.no_checksum"))
	case errors.Is(err, presets.ErrBadChecksum):
		return errors.New(i18n.T("presets.update.bad_checksum"))
	case errors.Is(err, presets.ErrChecksumMismatch):
		return errors.New(i18n.T("presets.update.mismatch"))
	}
	return err
}

// Running reports whether SplitDPI runs now, so a saved setting waits for a restart.
func (o *Ops) Running() bool {
	_, _, held := supervisor.LockInfo(o.P)
	return held
}

// RestartCommand is the command that restarts the bypass the way it runs now:
// the service through stop and enable, which starts it again, a background
// run in the background, anything else in the foreground.
func (o *Ops) RestartCommand() string {
	_, mode, _ := supervisor.LockInfo(o.P)
	switch mode {
	case "service":
		return "sudo splitdpi stop && sudo splitdpi enable"
	case "background":
		return "sudo splitdpi stop && sudo splitdpi run --background"
	}
	return "sudo splitdpi stop && sudo splitdpi run"
}

// updateConfig saves the config after f changes it; an error from f saves nothing.
func (o *Ops) updateConfig(f func(*config.Config) error) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	c, err := config.Load(o.P)
	if err != nil {
		return err
	}
	if err := f(&c); err != nil {
		return err
	}
	return config.Save(o.P, c)
}
