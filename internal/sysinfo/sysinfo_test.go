package sysinfo

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/KadirBerkpolat1/SplitDPI/internal/execx"
)

func eq[T comparable](t *testing.T, got, want T) {
	t.Helper()
	if got != want {
		t.Errorf("got %v, want %v", got, want)
	}
}

func releaseFixture(t *testing.T, name string) OSRelease {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", "os-release", name))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	o, err := ParseOSRelease(f)
	if err != nil {
		t.Fatal(err)
	}
	return o
}

func TestDetectPkgManager(t *testing.T) {
	root := os.DirFS("testdata")
	cases := []struct {
		fixture string
		want    PkgManager
		extra   fstest.MapFS
	}{
		{"debian13", Apt, nil},
		{"fedora", Dnf, nil},
		{"silverblue", RpmOstree, fstest.MapFS{"run/ostree-booted": &fstest.MapFile{}}},
		{"cachyos", Pacman, nil},
		{"tumbleweed", Zypper, nil},
		{"alpine", Apk, nil},
		{"void", Xbps, nil},
		{"gentoo", Emerge, nil},
	}
	for _, tc := range cases {
		t.Run(tc.fixture, func(t *testing.T) {
			o := releaseFixture(t, tc.fixture)
			fsys := fs.FS(root)
			if tc.extra != nil {
				fsys = tc.extra
			}
			eq(t, DetectPkgManager(o, fsys), tc.want)
		})
	}
}

func TestDetectInit(t *testing.T) {
	cases := []struct {
		name string
		fs   fstest.MapFS
		want Init
	}{
		{"systemd", fstest.MapFS{"run/systemd/system/.keep": &fstest.MapFile{}}, Systemd},
		{"openrc", fstest.MapFS{"run/openrc/.keep": &fstest.MapFile{}}, OpenRC},
		{"runit", fstest.MapFS{"etc/runit/.keep": &fstest.MapFile{}, "var/service/.keep": &fstest.MapFile{}}, Runit},
		{"other", fstest.MapFS{}, OtherInit},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			eq(t, DetectInit(tc.fs), tc.want)
		})
	}
}

func TestInstallHint(t *testing.T) {
	cases := []struct {
		pm   PkgManager
		pkgs []string
		want string
	}{
		{Apt, []string{"nftables", "curl"}, "sudo apt install nftables curl"},
		{Dnf, []string{"nftables", "curl"}, "sudo dnf install nftables curl"},
		{RpmOstree, []string{"nftables"}, "sudo rpm-ostree install nftables && sudo systemctl reboot"},
		{Pacman, []string{"nftables", "curl"}, "sudo pacman -S nftables curl"},
		{Zypper, []string{"nftables", "curl"}, "sudo zypper install nftables curl"},
		{Apk, []string{"nftables", "curl"}, "sudo apk add nftables curl"},
		{Xbps, []string{"nftables", "curl"}, "sudo xbps-install nftables curl"},
		{Emerge, []string{"nftables", "curl"}, "sudo emerge net-firewall/nftables net-misc/curl"},
		{Unknown, []string{"nftables", "curl"}, "install with your package manager: nftables curl"},
	}
	for _, tc := range cases {
		t.Run(string(tc.pm), func(t *testing.T) {
			eq(t, InstallHint(tc.pm, tc.pkgs...), tc.want)
		})
	}
}

func TestDetectLSM(t *testing.T) {
	enforcing := DetectLSM(fstest.MapFS{
		"sys/fs/selinux/enforce": &fstest.MapFile{Data: []byte("1\n")},
	})
	eq(t, enforcing.SELinux, "enforcing")

	aa := DetectLSM(fstest.MapFS{
		"sys/module/apparmor/parameters/enabled": &fstest.MapFile{Data: []byte("Y\n")},
	})
	if !aa.AppArmor {
		t.Error("AppArmor want true")
	}
}

func TestKernelAtLeast(t *testing.T) {
	ok, err := KernelAtLeast("6.17.3-cachyos", 5, 4)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Error("want true")
	}
	ok, err = KernelAtLeast("5.3.0", 5, 4)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("want false")
	}
}

func TestHasGlobalIPv6(t *testing.T) {
	const (
		route = "default via fe80::1 dev eth0 proto ra metric 100 pref medium\n"
		addr  = "2: eth0    inet6 2001:db8::5/64 scope global dynamic noprefixroute \\       valid_lft 2313068sec preferred_lft 2226668sec\n"
	)
	fake := func(routeOut, addrOut string) execx.Runner {
		return func(ctx context.Context, stdin string, name string, args ...string) ([]byte, error) {
			switch {
			case name == "ip" && len(args) > 1 && args[0] == "-6" && args[1] == "route":
				return []byte(routeOut), nil
			case name == "ip" && len(args) > 1 && args[0] == "-6" && args[1] == "addr":
				return []byte(addrOut), nil
			}
			return nil, io.ErrUnexpectedEOF
		}
	}
	for _, c := range []struct {
		name        string
		route, addr string
		want        bool
	}{
		{"route and global address", route, addr, true},
		{"route but only link-local", route, "", false},
		{"global address but no route", "", addr, false},
		{"neither", "", "", false},
		{"only a tentative address", route, "2: eth0    inet6 2001:db8::5/64 scope global tentative dynamic \\       valid_lft forever preferred_lft forever\n", false},
		{"only a deprecated address", route, "    inet6 2001:db8::5/64 scope global deprecated dynamic\n", false},
		{"a deprecated and a usable address", route, "    inet6 2001:db8::5/64 scope global deprecated dynamic\n    inet6 2001:db8::6/64 scope global dynamic\n", true},
	} {
		if got := HasGlobalIPv6(context.Background(), fake(c.route, c.addr)); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
	fail := execx.Runner(func(context.Context, string, string, ...string) ([]byte, error) {
		return nil, io.ErrUnexpectedEOF
	})
	if HasGlobalIPv6(context.Background(), fail) {
		t.Error("want false when ip fails")
	}
}

// BusyBox ip (Alpine without iproute2) rejects address flag filters such as
// -tentative: `ip: either "dev" is duplicate, or "-deprecated" is garbage`.
func TestHasGlobalIPv6BusyBox(t *testing.T) {
	busybox := execx.Runner(func(_ context.Context, _ string, name string, args ...string) ([]byte, error) {
		if name != "ip" || len(args) < 2 || args[0] != "-6" {
			return nil, io.ErrUnexpectedEOF
		}
		for _, a := range args[1:] {
			if strings.HasPrefix(a, "-") {
				return nil, fmt.Errorf("ip: either \"dev\" is duplicate, or %q is garbage", a)
			}
		}
		if args[1] == "route" {
			return []byte("default via fe80::1 dev eth0 metric 1024\n"), nil
		}
		return []byte("2: eth0: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1500 qlen 1000\n    inet6 2001:db8::5/64 scope global dynamic\n       valid_lft 86381sec preferred_lft 14381sec\n"), nil
	})
	if !HasGlobalIPv6(context.Background(), busybox) {
		t.Error("want true with BusyBox ip and a global address")
	}
}

func TestParseOSReleaseIDLike(t *testing.T) {
	o, err := ParseOSRelease(strings.NewReader("ID=arch\nID_LIKE=arch\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(o.IDLike) != 1 || o.IDLike[0] != "arch" {
		t.Fatalf("IDLike=%v", o.IDLike)
	}
}
