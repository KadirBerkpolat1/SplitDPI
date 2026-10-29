package scan_test

import (
	"strings"
	"testing"

	"github.com/KadirBerkpolat1/SplitDPI/internal/scan"
)

const (
	tlsSimple  = "--payload=tls_client_hello --lua-desync=multisplit:pos=1"
	tlsDisord  = "--payload=tls_client_hello --lua-desync=multidisorder:pos=2:seqovl=1"
	quicSimple = "--payload=quic_initial --lua-desync=fake:blob=fake_default_quic:repeats=6"
)

// siteLog is a one-domain blockcheck2 run: no * COMMON or * COVERAGE section.
func siteLog(summary ...string) string {
	return strings.Join(append([]string{
		"* curl_test_https_tls12 ipv4 pastebin.com",
		"- checking without DPI bypass",
		"!!!!! curl_test_https_tls12: working strategy found for ipv4 pastebin.com : nfqws2 " + tlsSimple + " !!!!!",
		"",
		"* SUMMARY",
	}, append(summary,
		"",
		"Please note this SUMMARY does not guarantee a magic pill for you to copy/paste and be happy.",
		"Understanding how strategies work is very desirable.",
	)...), "\n")
}

func TestParseSiteIPv4Only(t *testing.T) {
	res, err := scan.ParseBlockcheck(siteLog(
		"curl_test_https_tls12 ipv4 pastebin.com : nfqws2 "+tlsSimple,
		"curl_test_https_tls12 ipv4 pastebin.com : nfqws2 "+tlsDisord,
	))
	if err != nil {
		t.Fatal(err)
	}
	if res.TCPTLS != tlsSimple || res.QUIC != "" {
		t.Errorf("TCPTLS %q QUIC %q, want the first working TLS strategy", res.TCPTLS, res.QUIC)
	}
}

// After an IPv4+IPv6 run the simplest strategy that works on both wins, even
// if a simpler one works on IPv4 alone.
func TestParseSitePrefersBothIPVersions(t *testing.T) {
	res, err := scan.ParseBlockcheck(siteLog(
		"curl_test_https_tls12 ipv4 pastebin.com : nfqws2 "+tlsSimple,
		"curl_test_https_tls12 ipv4 pastebin.com : nfqws2 "+tlsDisord,
		"curl_test_http3 ipv4 pastebin.com : nfqws2 "+quicSimple,
		"curl_test_https_tls12 ipv6 pastebin.com : nfqws2 "+tlsDisord,
		"curl_test_http3 ipv6 pastebin.com : nfqws2 "+quicSimple,
	))
	if err != nil {
		t.Fatal(err)
	}
	if res.TCPTLS != tlsDisord {
		t.Errorf("TCPTLS %q, want %q (works on IPv4 and IPv6)", res.TCPTLS, tlsDisord)
	}
	if res.QUIC != quicSimple {
		t.Errorf("QUIC %q, want %q", res.QUIC, quicSimple)
	}
}

func TestParseSiteNothingCommonFallsBack(t *testing.T) {
	res, err := scan.ParseBlockcheck(siteLog(
		"curl_test_https_tls12 ipv4 pastebin.com : nfqws2 "+tlsSimple,
		"curl_test_https_tls12 ipv6 pastebin.com : nfqws2 not working",
	))
	if err != nil {
		t.Fatal(err)
	}
	if res.TCPTLS != tlsSimple {
		t.Errorf("TCPTLS %q, want the first working one %q", res.TCPTLS, tlsSimple)
	}
}

func TestParseSiteNoneWorking(t *testing.T) {
	res, err := scan.ParseBlockcheck(siteLog(
		"curl_test_https_tls12 ipv4 pastebin.com : nfqws2 not working",
		"curl_test_https_tls12 ipv6 pastebin.com : working without bypass",
	))
	if err != nil {
		t.Fatal(err)
	}
	if res.TCPTLS != "" {
		t.Errorf("TCPTLS %q, want none", res.TCPTLS)
	}
}
