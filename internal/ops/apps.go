package ops

import (
	"errors"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/KadirBerkpolat1/SplitDPI/internal/config"
	"github.com/KadirBerkpolat1/SplitDPI/internal/domain"
	"github.com/KadirBerkpolat1/SplitDPI/internal/presets"
)

// Editing an app saves the user's copy of it in P.UserApps; AppReset drops
// the copy again.

// AppExistsError is returned for a new app whose id is taken.
type AppExistsError struct{ ID string }

func (e AppExistsError) Error() string { return "app " + e.ID + " exists" }

// ErrNoUDP is returned for turning on the voice and stream (UDP) profile of
// an app that never had one.
var ErrNoUDP = errors.New("app has no voice and stream profile")

// App returns the app id as it runs: the user's copy if there is one.
func (o *Ops) App(id string) (presets.App, error) {
	set := o.PresetsList()
	a, ok := set.Apps[id]
	if !ok {
		return a, UnknownAppError{ID: id, Valid: slices.Sorted(maps.Keys(set.Apps))}
	}
	return a, nil
}

// AppNew saves a new app of the user's without addresses and enables it.
// An empty name means the id.
func (o *Ops) AppNew(id, name string) error {
	if _, ok := o.PresetsList().Apps[id]; ok {
		return AppExistsError{ID: id}
	}
	if name == "" {
		name = id
	}
	if err := presets.SaveUser(o.P.UserApps, presets.App{ID: id, Name: name}); err != nil {
		return err
	}
	return o.AppsEnable(id)
}

// AppHostAdd adds the normalized domain to the app's addresses and returns
// it. Like SitesAdd, it clears the local DNS caches while SplitDPI runs with
// encrypted DNS.
func (o *Ops) AppHostAdd(id, d string) (string, error) {
	a, err := o.App(id)
	if err != nil {
		return "", err
	}
	n, err := domain.Normalize(d)
	if err != nil {
		return "", err
	}
	if slices.Contains(a.Hosts, n) {
		return n, nil
	}
	a.Hosts = append(slices.Clone(a.Hosts), n)
	slices.Sort(a.Hosts)
	if err := presets.SaveUser(o.P.UserApps, a); err != nil {
		return n, err
	}
	o.flushRunningDNS()
	return n, nil
}

func (o *Ops) AppHostRemove(id, d string) error {
	a, err := o.App(id)
	if err != nil {
		return err
	}
	n, err := domain.Normalize(d)
	if err != nil {
		return err
	}
	if !slices.Contains(a.Hosts, n) {
		return nil
	}
	a.Hosts = slices.DeleteFunc(slices.Clone(a.Hosts), func(h string) bool { return h == n })
	c, err := config.Load(o.P)
	if err != nil {
		return err
	}
	if err := o.checkAppsMode(c, a, ""); err != nil {
		return err
	}
	return presets.SaveUser(o.P.UserApps, a)
}

// AppSetUDP turns the app's voice and stream (UDP) profile off, or back on
// as its preset has it.
func (o *Ops) AppSetUDP(id string, on bool) error {
	a, err := o.App(id)
	if err != nil {
		return err
	}
	switch {
	case on == (a.UDP != nil):
		return nil
	case !on:
		a.UDP = nil
	case a.Preset != nil && a.Preset.UDP != nil:
		a.UDP = a.Preset.UDP
	default:
		return ErrNoUDP
	}
	return presets.SaveUser(o.P.UserApps, a)
}

// AppReset drops the user's copy of the app: a shipped app is back as
// shipped, and an app of the user's own is deleted and disabled.
func (o *Ops) AppReset(id string) error {
	a, err := o.App(id)
	if err != nil || !a.User {
		return err
	}
	c, err := config.Load(o.P)
	if err != nil {
		return err
	}
	after := presets.App{}
	if a.Preset != nil {
		after = *a.Preset
	} else {
		c.Apps = slices.DeleteFunc(c.Apps, func(x string) bool { return x == id })
	}
	if err := o.checkAppsMode(c, after, ""); err != nil {
		return err
	}
	if err := presets.RemoveUser(o.P.UserApps, id); err != nil {
		return err
	}
	if a.Preset == nil {
		return o.AppsDisable(id)
	}
	return nil
}

var idFold = strings.NewReplacer("ç", "c", "ğ", "g", "ı", "i", "ö", "o", "ş", "s", "ü", "u")

// NewAppID makes an id for a new app called name that is not taken: its
// letters and digits with dashes between words, and a number after it if
// that is taken.
func NewAppID(name string, taken func(id string) bool) string {
	var b strings.Builder
	gap := false
	for _, r := range idFold.Replace(strings.ToLower(strings.ReplaceAll(name, "İ", "i"))) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			if gap && b.Len() > 0 {
				b.WriteByte('-')
			}
			b.WriteRune(r)
			gap = false
		} else {
			gap = true
		}
	}
	base := b.String()
	if base == "" {
		base = "app"
	}
	id := base
	for n := 2; ; n++ {
		if !taken(id) {
			return id
		}
		id = base + "-" + strconv.Itoa(n)
	}
}
