package sysinfo

import (
	"strings"
	"testing"
	"testing/fstest"
)

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
