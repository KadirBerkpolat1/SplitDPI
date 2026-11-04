package sysinfo

import (
	"io/fs"
	"strings"
)

type PkgManager string

const (
	Apt       PkgManager = "apt"
	Dnf       PkgManager = "dnf"
	RpmOstree PkgManager = "rpm-ostree"
	Pacman    PkgManager = "pacman"
	Zypper    PkgManager = "zypper"
	Apk       PkgManager = "apk"
	Xbps      PkgManager = "xbps"
	Emerge    PkgManager = "emerge"
	Unknown   PkgManager = "unknown"
)

func DetectPkgManager(o OSRelease, root fs.FS) PkgManager {
	if fileExists(root, "run/ostree-booted") && o.ID == "fedora" {
		return RpmOstree
	}
	if o.ID == "debian" || o.ID == "ubuntu" || hasLike(o, "debian") {
		return Apt
	}
	if o.ID == "fedora" || hasLike(o, "fedora", "rhel") {
		return Dnf
	}
	if o.ID == "arch" || o.ID == "cachyos" || hasLike(o, "arch") {
		return Pacman
	}
	if strings.HasPrefix(o.ID, "opensuse") || o.ID == "sles" || hasLike(o, "suse") {
		return Zypper
	}
	if o.ID == "alpine" {
		return Apk
	}
	if o.ID == "void" {
		return Xbps
	}
	if o.ID == "gentoo" {
		return Emerge
	}
	return Unknown
}

func hasLike(o OSRelease, likes ...string) bool {
	for _, l := range o.IDLike {
		for _, want := range likes {
			if l == want {
				return true
			}
		}
	}
	return false
}

func fileExists(fsys fs.FS, path string) bool {
	_, err := fs.Stat(fsys, path)
	return err == nil
}

func InstallHint(pm PkgManager, pkgs ...string) string {
	joined := strings.Join(pkgs, " ")
	switch pm {
	case Apt:
		return "sudo apt install " + joined
	case Dnf:
		return "sudo dnf install " + joined
	case RpmOstree:
		return "sudo rpm-ostree install " + joined + " && sudo systemctl reboot"
	case Pacman:
		return "sudo pacman -S " + joined
	case Zypper:
		return "sudo zypper install " + joined
	case Apk:
		return "sudo apk add " + joined
	case Xbps:
		return "sudo xbps-install " + joined
	case Emerge:
		return "sudo emerge " + joined
	case Unknown:
		return "install with your package manager: " + joined
	default:
		return "install with your package manager: " + joined
	}
}

// RemoveHint is the command that removes pkgs again.
func RemoveHint(pm PkgManager, pkgs ...string) string {
	joined := strings.Join(pkgs, " ")
	switch pm {
	case Apt:
		return "sudo apt remove " + joined
	case Dnf:
		return "sudo dnf remove " + joined
	case RpmOstree:
		return "sudo rpm-ostree uninstall " + joined + " && sudo systemctl reboot"
	case Pacman:
		return "sudo pacman -Rs " + joined
	case Zypper:
		return "sudo zypper remove " + joined
	case Apk:
		return "sudo apk del " + joined
	case Xbps:
		return "sudo xbps-remove -R " + joined
	case Emerge:
		return "sudo emerge --deselect " + joined + " && sudo emerge --depclean"
	}
	return "remove with your package manager: " + joined
}

// Tool is a command splitdpi or blockcheck2 runs; any of Commands will do.
type Tool struct {
	ID       string
	Commands []string
}

var (
	// Nft adds splitdpi's firewall rules; nothing runs without it.
	Nft = Tool{ID: "nft", Commands: []string{"nft"}}
	// Curl and Lookup are what blockcheck2 needs besides nft: it tests sites
	// with curl and checks DNS with nslookup or host.
	Curl   = Tool{ID: "curl", Commands: []string{"curl"}}
	Lookup = Tool{ID: "lookup", Commands: []string{"nslookup", "host"}}
)

// Tools is every command splitdpi needs, in the order doctor checks them.
var Tools = []Tool{Nft, Curl, Lookup}

// toolPackages names the package that installs a tool; "" is the name for
// every other package manager.
var toolPackages = map[string]map[PkgManager]string{
	"nft":  {"": "nftables", Emerge: "net-firewall/nftables"},
	"curl": {"": "curl", Emerge: "net-misc/curl"},
	"lookup": {
		"": "nslookup", Apt: "bind9-host", Dnf: "bind-utils", RpmOstree: "bind-utils", Pacman: "bind",
		Zypper: "bind-utils", Apk: "bind-tools", Xbps: "bind-utils",
		// net-dns/bind-tools is a masked stub; the client tools ship with the server.
		Emerge: "net-dns/bind",
	},
}

// Package names what installs t with pm.
func (t Tool) Package(pm PkgManager) string {
	if p, ok := toolPackages[t.ID][pm]; ok {
		return p
	}
	return toolPackages[t.ID][""]
}

// InstallCommand installs packages as root without asking. Refresh, when set,
// updates the package lists first; Install may still work when it fails.
type InstallCommand struct {
	Refresh []string
	Install []string
}

// Installer returns how pm installs pkgs; ok is false when pm is Unknown.
func Installer(pm PkgManager, pkgs []string) (InstallCommand, bool) {
	with := func(argv ...string) []string { return append(argv, pkgs...) }
	switch pm {
	case Apt:
		return InstallCommand{Refresh: []string{"apt-get", "update"}, Install: with("apt-get", "install", "-y")}, true
	case Dnf:
		return InstallCommand{Install: with("dnf", "install", "-y")}, true
	case RpmOstree:
		return InstallCommand{Install: with("rpm-ostree", "install", "--idempotent", "--apply-live")}, true
	case Pacman:
		// No -y: refreshing the databases without upgrading is a partial upgrade.
		return InstallCommand{Install: with("pacman", "-S", "--needed", "--noconfirm")}, true
	case Zypper:
		return InstallCommand{Install: with("zypper", "--non-interactive", "install")}, true
	case Apk:
		return InstallCommand{Install: with("apk", "add")}, true
	case Xbps:
		return InstallCommand{Install: with("xbps-install", "-Sy")}, true
	case Emerge:
		// --ask=n: EMERGE_DEFAULT_OPTS may hold --ask, and nobody answers it.
		return InstallCommand{Install: with("emerge", "--ask=n", "--noreplace")}, true
	}
	return InstallCommand{}, false
}

// Packages install the tools a machine lacks.
type Packages struct {
	Manager PkgManager
	Tools   []Tool   // the missing tools
	Names   []string // the package of each tool
}

func NewPackages(pm PkgManager, tools []Tool) Packages {
	p := Packages{Manager: pm, Tools: tools}
	for _, t := range tools {
		p.Names = append(p.Names, t.Package(pm))
	}
	return p
}

// CanInstall reports whether splitdpi knows how to install them here.
func (p Packages) CanInstall() bool {
	_, ok := Installer(p.Manager, p.Names)
	return ok
}

// Command is what installing them runs, on one line; "" when it cannot.
func (p Packages) Command() string {
	c, ok := Installer(p.Manager, p.Names)
	if !ok {
		return ""
	}
	line := strings.Join(c.Install, " ")
	if len(c.Refresh) > 0 {
		line = strings.Join(c.Refresh, " ") + "; " + line
	}
	return line
}

// Hint is the command that installs them by hand.
func (p Packages) Hint() string { return InstallHint(p.Manager, p.Names...) }

// RetryHint is the command to run by hand once installing them failed. When
// pacman -S fails, the package database is mostly stale, and only a full
// upgrade fixes that.
func (p Packages) RetryHint() string {
	if p.Manager == Pacman {
		return "sudo pacman -Syu " + strings.Join(p.Names, " ")
	}
	return p.Hint()
}

// RemoveHint is the command that removes them again.
func (p Packages) RemoveHint() string { return RemoveHint(p.Manager, p.Names...) }
