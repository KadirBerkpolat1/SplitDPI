package dns

import "testing"

func TestIsLocalName(t *testing.T) {
	search := []string{"corp.example", "home"}
	cases := map[string]bool{
		"printer.":                  true,
		"printer":                   true,
		"PRINTER.LAN.":              true,
		"printer.lan.":              true,
		"nas.local.":                true,
		"router.home.arpa.":         true,
		"svc.internal.":             true,
		"box.localdomain.":          true,
		"lan.":                      true,
		"git.corp.example.":         true,
		"corp.example.":             true,
		"tv.home.":                  true,
		"5.1.168.192.in-addr.arpa.": true,
		"168.192.in-addr.arpa.":     true,
		"1.0.0.10.in-addr.arpa.":    true,
		"10.in-addr.arpa.":          true,
		"1.0.16.172.in-addr.arpa.":  true,
		"1.0.31.172.in-addr.arpa.":  true,
		"1.0.254.169.in-addr.arpa.": true,
		"1.0.64.100.in-addr.arpa.":  true,
		"1.0.127.100.in-addr.arpa.": true,
		"1.0.0.127.in-addr.arpa.":   true,
		"1.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.d.f.ip6.arpa.": true,
		"1.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.8.e.f.ip6.arpa.": true,
		"1.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.ip6.arpa.": true,

		".":                         false,
		"example.com.":              false,
		"foo.lan.example.com.":      false,
		"plan.":                     true, // single label
		"my.plan.":                  false,
		"notcorp.example.":          false,
		"1.0.0.1.in-addr.arpa.":     false,
		"8.8.8.8.in-addr.arpa.":     false,
		"1.0.32.172.in-addr.arpa.":  false,
		"1.0.128.100.in-addr.arpa.": false,
		"172.in-addr.arpa.":         false, // wider than 172.16/12
		"in-addr.arpa.":             false,
		"x.1.168.192.in-addr.arpa.": false,
		"1.1.1.1.1.in-addr.arpa.":   false,
		"1.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.2.ip6.arpa.": false,
		"e.f.ip6.arpa.": false, // fe00::/8 is wider than fe80::/10
		"2.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.ip6.arpa.": false,
	}
	for name, want := range cases {
		if got := isLocalName(name, search); got != want {
			t.Errorf("%q: got %v, want %v", name, got, want)
		}
	}
}
