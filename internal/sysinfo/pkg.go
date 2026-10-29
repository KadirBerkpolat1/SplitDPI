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
		var atoms []string
		for _, p := range pkgs {
			atoms = append(atoms, emergeAtom(p))
		}
		return "sudo emerge " + strings.Join(atoms, " ")
	case Unknown:
		return "install with your package manager: " + joined
	default:
		return "install with your package manager: " + joined
	}
}

func emergeAtom(pkg string) string {
	switch pkg {
	case "nftables":
		return "net-firewall/nftables"
	case "curl":
		return "net-misc/curl"
	default:
		return pkg
	}
}
