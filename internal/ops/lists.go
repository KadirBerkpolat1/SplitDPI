package ops

import (
	"maps"
	"os"
	"slices"

	"github.com/KadirBerkpolat1/SplitDPI/internal/config"
	"github.com/KadirBerkpolat1/SplitDPI/internal/domain"
	"github.com/KadirBerkpolat1/SplitDPI/internal/presets"
)

// UnknownAppError is returned for an app id that no preset defines.
type UnknownAppError struct {
	ID    string
	Valid []string // sorted
}

func (e UnknownAppError) Error() string { return "unknown app " + e.ID }

func (o *Ops) PresetsList() presets.Set {
	return presets.Current(o.P)
}

// AppsList returns the known apps sorted by id and the ids enabled in config.
func (o *Ops) AppsList() ([]presets.App, []string, error) {
	c, err := config.Load(o.P)
	if err != nil {
		return nil, nil, err
	}
	set := o.PresetsList()
	apps := make([]presets.App, 0, len(set.Apps))
	for _, id := range slices.Sorted(maps.Keys(set.Apps)) {
		apps = append(apps, set.Apps[id])
	}
	return apps, c.Apps, nil
}

func (o *Ops) AppsEnable(id string) error {
	set := o.PresetsList()
	if _, ok := set.Apps[id]; !ok {
		return UnknownAppError{ID: id, Valid: slices.Sorted(maps.Keys(set.Apps))}
	}
	c, err := config.Load(o.P)
	if err != nil {
		return err
	}
	if slices.Contains(c.Apps, id) {
		return nil
	}
	c.Apps = append(c.Apps, id)
	return config.Save(o.P, c)
}

func (o *Ops) AppsDisable(id string) error {
	c, err := config.Load(o.P)
	if err != nil {
		return err
	}
	if !slices.Contains(c.Apps, id) {
		return nil
	}
	c.Apps = slices.DeleteFunc(c.Apps, func(a string) bool { return a == id })
	return config.Save(o.P, c)
}

// AcceptDisclaimer records that the first-start notice was accepted.
func (o *Ops) AcceptDisclaimer() error {
	c, err := config.Load(o.P)
	if err != nil {
		return err
	}
	c.DisclaimerAccepted = true
	return config.Save(o.P, c)
}

func (o *Ops) ExcludeList() ([]string, error) {
	return config.ReadList(o.P.UserExclude)
}

// ExcludeAdd adds the normalized domain to the user exclude list and drops it
// from the learned autohostlist. It returns the normalized domain.
func (o *Ops) ExcludeAdd(d string) (string, error) {
	n, err := domain.Normalize(d)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(o.P.ConfigDir, 0o755); err != nil {
		return "", err
	}
	if err := config.AddToList(o.P.UserExclude, n); err != nil {
		return "", err
	}
	return n, config.RemoveFromList(o.P.AutoHostlist, n)
}

func (o *Ops) ExcludeRemove(d string) error {
	n, err := domain.Normalize(d)
	if err != nil {
		return err
	}
	return config.RemoveFromList(o.P.UserExclude, n)
}

// SitesList returns the user's own sites, which the bypass always handles.
func (o *Ops) SitesList() ([]string, error) {
	return config.ReadList(o.P.UserHosts)
}

// SitesAdd adds the normalized domain to the user's sites and returns it.
func (o *Ops) SitesAdd(d string) (string, error) {
	n, err := domain.Normalize(d)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(o.P.ConfigDir, 0o755); err != nil {
		return "", err
	}
	return n, config.AddToList(o.P.UserHosts, n)
}

func (o *Ops) SitesRemove(d string) error {
	n, err := domain.Normalize(d)
	if err != nil {
		return err
	}
	return config.RemoveFromList(o.P.UserHosts, n)
}
