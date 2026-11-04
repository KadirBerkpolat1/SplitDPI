package sysinfo

import (
	"strings"
	"testing"
	"testing/fstest"
)

// Each distro gets the package that really ships nslookup or host.
func TestToolPackage(t *testing.T) {
	for pm, want := range map[PkgManager]string{
		Apt: "bind9-host", Dnf: "bind-utils", RpmOstree: "bind-utils", Pacman: "bind",
		Zypper: "bind-utils", Apk: "bind-tools", Xbps: "bind-utils", Emerge: "net-dns/bind",
		Unknown: "nslookup",
	} {
		eq(t, Lookup.Package(pm), want)
	}
	eq(t, Nft.Package(Pacman), "nftables")
	eq(t, Nft.Package(Emerge), "net-firewall/nftables")
	eq(t, Curl.Package(Apt), "curl")
	eq(t, Curl.Package(Emerge), "net-misc/curl")
}

// Installs run without a question: the TUI has no terminal to answer one.
func TestInstaller(t *testing.T) {
	pkgs := []string{"curl", "bind"}
	for _, tc := range []struct {
		pm               PkgManager
		refresh, install string
	}{
		{Apt, "apt-get update", "apt-get install -y curl bind"},
		{Dnf, "", "dnf install -y curl bind"},
		{RpmOstree, "", "rpm-ostree install --idempotent --apply-live curl bind"},
		{Pacman, "", "pacman -S --needed --noconfirm curl bind"},
		{Zypper, "", "zypper --non-interactive install curl bind"},
		{Apk, "", "apk add curl bind"},
		{Xbps, "", "xbps-install -Sy curl bind"},
		{Emerge, "", "emerge --noreplace curl bind"},
	} {
		t.Run(string(tc.pm), func(t *testing.T) {
			c, ok := Installer(tc.pm, pkgs)
			if !ok {
				t.Fatal("not installable")
			}
			eq(t, strings.Join(c.Refresh, " "), tc.refresh)
			eq(t, strings.Join(c.Install, " "), tc.install)
		})
	}
	if _, ok := Installer(Unknown, pkgs); ok {
		t.Error("an unknown package manager installs")
	}
}

func TestPackages(t *testing.T) {
	p := NewPackages(Apt, []Tool{Curl, Lookup})
	eq(t, strings.Join(p.Names, " "), "curl bind9-host")
	eq(t, p.CanInstall(), true)
	eq(t, p.Command(), "apt-get update; apt-get install -y curl bind9-host")
	eq(t, p.Hint(), "sudo apt install curl bind9-host")
	eq(t, p.RemoveHint(), "sudo apt remove curl bind9-host")

	eq(t, NewPackages(Pacman, []Tool{Lookup}).Command(), "pacman -S --needed --noconfirm bind")
	eq(t, NewPackages(RpmOstree, []Tool{Lookup}).RemoveHint(), "sudo rpm-ostree uninstall bind-utils && sudo systemctl reboot")

	u := NewPackages(Unknown, []Tool{Nft, Lookup})
	eq(t, u.CanInstall(), false)
	eq(t, u.Command(), "")
	eq(t, u.Hint(), "install with your package manager: nftables nslookup")
}

func TestReadOSRelease(t *testing.T) {
	eq(t, ReadOSRelease(fstest.MapFS{"usr/lib/os-release": {Data: []byte("ID=arch\n")}}).ID, "arch")
	eq(t, ReadOSRelease(fstest.MapFS{}).ID, "")
}
