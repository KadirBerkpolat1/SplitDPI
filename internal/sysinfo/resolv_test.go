package sysinfo

import (
	"fmt"
	"testing"
	"testing/fstest"
)

const routeHeader = "Iface\tDestination\tGateway \tFlags\tRefCnt\tUse\tMetric\tMask\t\tMTU\tWindow\tIRTT\n"

// 192.168.1.1 via wlan0 (metric 600) and 10.0.0.1 via eth0 (metric 100).
const routes = routeHeader +
	"wlan0\t00000000\t0101A8C0\t0003\t0\t0\t600\t00000000\t0\t0\t0\n" +
	"wlan0\t0001A8C0\t00000000\t0001\t0\t0\t600\t00FFFFFF\t0\t0\t0\n" +
	"eth0\t00000000\t0100000A\t0003\t0\t0\t100\t00000000\t0\t0\t0\n"

func file(s string) *fstest.MapFile { return &fstest.MapFile{Data: []byte(s)} }

func TestReadLocalDNSOrder(t *testing.T) {
	stub := "nameserver 127.0.0.53\noptions edns0 trust-ad\nsearch lan\n"
	cases := []struct {
		name  string
		files fstest.MapFS
		want  string
	}{
		{"resolved upstreams first", fstest.MapFS{
			"run/systemd/resolve/resolv.conf":        file("nameserver 192.168.1.1\nnameserver fd00::1\nsearch lan\n"),
			"run/NetworkManager/no-stub-resolv.conf": file("nameserver 10.9.9.9\n"),
			"etc/resolv.conf":                        file(stub),
			"proc/net/route":                         file(routes),
		}, "[192.168.1.1 fd00::1] [lan]"},
		{"resolved behind a local proxy: NetworkManager next", fstest.MapFS{
			"run/systemd/resolve/resolv.conf":        file("nameserver 127.0.0.1\nsearch corp.example\n"),
			"run/NetworkManager/no-stub-resolv.conf": file("nameserver 10.9.9.9\nsearch Home.Lan.\n"),
			"run/NetworkManager/resolv.conf":         file("nameserver 10.8.8.8\n"),
			"etc/resolv.conf":                        file(stub),
		}, "[10.9.9.9] [corp.example home.lan lan]"},
		{"NetworkManager stub file", fstest.MapFS{
			"run/NetworkManager/resolv.conf": file("nameserver 10.8.8.8\n"),
			"etc/resolv.conf":                file("nameserver 1.1.1.1\n"),
		}, "[10.8.8.8] []"},
		{"plain /etc/resolv.conf", fstest.MapFS{
			"etc/resolv.conf": file("# dhcp\ndomain office\nnameserver ::1\nnameserver 192.168.0.1\n"),
		}, "[192.168.0.1] [office]"},
		{"only loopback: default gateway with the lowest metric", fstest.MapFS{
			"run/systemd/resolve/resolv.conf": file("nameserver 127.0.0.1\n"),
			"etc/resolv.conf":                 file(stub + "nameserver ::1\nnameserver ::ffff:127.0.0.1\n"),
			"proc/net/route":                  file(routes),
		}, "[10.0.0.1] [lan]"},
		{"routing-only domains skipped", fstest.MapFS{
			"run/systemd/resolve/resolv.conf": file("nameserver 192.168.1.1\nsearch ~. ~corp lan\n"),
		}, "[192.168.1.1] [lan]"},
		{"nothing at all", fstest.MapFS{
			"etc/resolv.conf": file(stub),
			"proc/net/route":  file(routeHeader + "wlan0\t0001A8C0\t00000000\t0001\t0\t0\t600\t00FFFFFF\t0\t0\t0\n"),
		}, "[] [lan]"},
	}
	for _, c := range cases {
		d := ReadLocalDNS(c.files)
		if got := fmt.Sprint(d.Servers, " ", d.Search); got != c.want {
			t.Errorf("%s: got %s, want %s", c.name, got, c.want)
		}
	}
}
