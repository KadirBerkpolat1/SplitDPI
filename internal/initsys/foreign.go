package initsys

import (
	"context"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/KadirBerkpolat1/SplitDPI/internal/execx"
	"github.com/KadirBerkpolat1/SplitDPI/internal/sysinfo"
)

// ForeignService is another tool's service, controlled through its init
// system only: none of its files are written or removed. On runit the one
// change is its enable link in a runsvdir, which Disable removes and Enable
// recreates. Methods take root, the host root our paths live under.
type ForeignService struct {
	Init    sysinfo.Init `json:"init"`
	Name    string       `json:"name"`
	Enabled bool         `json:"enabled"` // starts on boot
	// BootUnknown: the init could not say whether it starts on boot.
	BootUnknown bool `json:"boot_unknown,omitempty"`
	// Runlevels are the OpenRC runlevels it is added to.
	Runlevels []string `json:"runlevels,omitempty"`
	// Link is the runit enable link (a host path) and Target what it points at.
	Link   string `json:"link,omitempty"`
	Target string `json:"target,omitempty"`
}

var systemdUnitDirs = []string{"etc/systemd/system", "usr/lib/systemd/system", "lib/systemd/system", "usr/local/lib/systemd/system"}

// systemdStates are the states systemctl is-enabled prints.
var systemdStates = []string{"enabled", "enabled-runtime", "linked", "linked-runtime", "alias", "masked", "masked-runtime",
	"static", "indirect", "disabled", "generated", "transient", "bad", "not-found"}

// MayStartOnBoot: it starts on boot, or the init could not say.
func (s ForeignService) MayStartOnBoot() bool { return s.Enabled || s.BootUnknown }

// FindForeign looks for the service name under init in on root (the host
// "/"); it returns the zero ForeignService when it is not installed there.
// A runit service counts only through an enable link: a service directory
// alone never runs.
func FindForeign(ctx context.Context, root fs.FS, run execx.Runner, in sysinfo.Init, name string) ForeignService {
	s := ForeignService{Init: in, Name: name}
	switch in {
	case sysinfo.Systemd:
		if !slices.ContainsFunc(systemdUnitDirs, func(d string) bool { return exists(root, d+"/"+name+".service") }) {
			return ForeignService{}
		}
		// is-enabled exits non-zero for every state but enabled; its stdout still
		// names the state. Without one (no bus, say) the state is unknown.
		out, _ := run(ctx, "", "systemctl", "is-enabled", name)
		state := strings.TrimSpace(string(out))
		s.Enabled = state == "enabled"
		s.BootUnknown = !slices.Contains(systemdStates, state)
	case sysinfo.OpenRC:
		if !exists(root, "etc/init.d/"+name) {
			return ForeignService{}
		}
		levels, _ := fs.Glob(root, "etc/runlevels/*/"+name)
		for _, l := range levels {
			s.Runlevels = append(s.Runlevels, path.Base(path.Dir(l)))
		}
		s.Enabled = len(s.Runlevels) > 0
	case sysinfo.Runit:
		links, _ := fs.Glob(root, "etc/runit/runsvdir/*/"+name)
		for _, l := range append(links, "var/service/"+name) {
			// runsvdir/current is a link to another runsvdir: skip the duplicate.
			if fi, err := fs.Lstat(root, path.Dir(l)); err != nil || fi.Mode()&fs.ModeSymlink != 0 {
				continue
			}
			if t, err := fs.ReadLink(root, l); err == nil {
				s.Link, s.Target, s.Enabled = "/"+l, t, true
				return s
			}
		}
		return ForeignService{}
	default:
		return ForeignService{}
	}
	return s
}

func (s ForeignService) Stop(ctx context.Context, run execx.Runner, root string) error {
	switch s.Init {
	case sysinfo.Systemd:
		_, err := run(ctx, "", "systemctl", "stop", s.Name)
		return err
	case sysinfo.OpenRC:
		_, err := run(ctx, "", "rc-service", s.Name, "stop")
		return err
	case sysinfo.Runit:
		_, err := run(ctx, "", "sv", "down", filepath.Join(root, s.Link))
		return err
	}
	return ErrUnsupportedInit
}

// Start starts the service. On runit it does nothing: runsvdir starts the
// service as soon as Enable has put its link back.
func (s ForeignService) Start(ctx context.Context, run execx.Runner, _ string) error {
	switch s.Init {
	case sysinfo.Systemd:
		_, err := run(ctx, "", "systemctl", "start", s.Name)
		return err
	case sysinfo.OpenRC:
		_, err := run(ctx, "", "rc-service", s.Name, "start")
		return err
	case sysinfo.Runit:
		return nil
	}
	return ErrUnsupportedInit
}

// Disable keeps the service from starting on boot. For one that does not, it
// changes nothing: systemctl disable leaves a disabled or static unit as it
// is, an OpenRC service in no runlevel has no rc-update del to run, and a
// runit service is only found through the enable link this removes.
func (s ForeignService) Disable(ctx context.Context, run execx.Runner, root string) error {
	switch s.Init {
	case sysinfo.Systemd:
		_, err := run(ctx, "", "systemctl", "disable", s.Name)
		return err
	case sysinfo.OpenRC:
		for _, l := range s.Runlevels {
			if _, err := run(ctx, "", "rc-update", "del", s.Name, l); err != nil {
				return err
			}
		}
		return nil
	case sysinfo.Runit:
		if err := os.Remove(filepath.Join(root, s.Link)); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	return ErrUnsupportedInit
}

func (s ForeignService) Enable(ctx context.Context, run execx.Runner, root string) error {
	switch s.Init {
	case sysinfo.Systemd:
		_, err := run(ctx, "", "systemctl", "enable", s.Name)
		return err
	case sysinfo.OpenRC:
		for _, l := range s.Runlevels {
			if _, err := run(ctx, "", "rc-update", "add", s.Name, l); err != nil {
				return err
			}
		}
		return nil
	case sysinfo.Runit:
		link := filepath.Join(root, s.Link)
		if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
			return err
		}
		if err := os.Symlink(s.Target, link); err != nil && !os.IsExist(err) {
			return err
		}
		return nil
	}
	return ErrUnsupportedInit
}

func exists(root fs.FS, name string) bool {
	_, err := fs.Stat(root, name)
	return err == nil
}
