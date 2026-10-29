package scan_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/KadirBerkpolat1/SplitDPI/internal/scan"
)

const (
	wantSimulateTLS = "--payload=tls_client_hello --lua-desync=tcpseg:pos=0,-1:seqovl=1 --lua-desync=drop"
	wantSimulateQUIC = "--payload=quic_initial --lua-desync=send:ipfrag:ipfrag_pos_udp=16 --lua-desync=drop"
	queueErr        = "nftables queue support is not available. pls install modules."
)

func readFixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// bc-real-tr.log is `splitdpi scan --site discord.com` on a Türk Telekom
// line (AS9121), 2026-10-29, IPVS=46, no HTTP3: discord.com has no AAAA record,
// so the ipv6 test aborts and the first ipv4 TLS line wins.
func TestParseReal(t *testing.T) {
	res, err := scan.ParseBlockcheck(readFixture(t, "bc-real-tr.log"))
	if err != nil {
		t.Fatal(err)
	}
	const wantTLS = "--payload=tls_client_hello --lua-desync=tcpseg:pos=0,1:ip_id=rnd:repeats=1"
	if res.TCPTLS != wantTLS {
		t.Fatalf("TCPTLS = %q, want %q", res.TCPTLS, wantTLS)
	}
	if res.QUIC != "" {
		t.Fatalf("QUIC = %q, want empty (HTTP3 was not tested)", res.QUIC)
	}
	if len(res.Coverage) != 0 {
		t.Fatalf("want no COVERAGE for one domain, got %d rows", len(res.Coverage))
	}
}

func TestParseSimulate(t *testing.T) {
	res, err := scan.ParseBlockcheck(readFixture(t, "bc-simulate.log"))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Summary) == 0 {
		t.Fatal("want SUMMARY lines")
	}
	if res.TCPTLS != wantSimulateTLS {
		t.Fatalf("TCPTLS = %q, want %q", res.TCPTLS, wantSimulateTLS)
	}
	if res.QUIC != wantSimulateQUIC {
		t.Fatalf("QUIC = %q, want %q", res.QUIC, wantSimulateQUIC)
	}
}

func TestParseNoSummary(t *testing.T) {
	_, err := scan.ParseBlockcheck(readFixture(t, "bc-noqueue.log"))
	if err == nil {
		t.Fatal("want error")
	}
	var bc scan.BlockcheckError
	if !errors.As(err, &bc) {
		t.Fatalf("want BlockcheckError, got %T: %v", err, err)
	}
	tail := strings.Join(bc.Tail, "\n")
	if !strings.Contains(tail, queueErr) {
		t.Fatalf("tail %q should contain queue error", tail)
	}
}

func TestParseSkipsInvalid(t *testing.T) {
	const good = "--payload=tls_client_hello --lua-desync=multidisorder:pos=2:seqovl=1"
	log := strings.Join([]string{
		"* SUMMARY",
		"curl_test_https_tls12 ipv4 example.com : nfqws2 " + good,
		"* COVERAGE",
		"2/2 : curl_test_https_tls12 ipv4 : nfqws2 --lua-init=x --payload=tls_client_hello --lua-desync=drop",
		"2/2 : curl_test_https_tls12 ipv4 : nfqws2 --payload=tls_client_hello --lua-desync=luaexec:code=x --lua-desync=drop",
		"2/2 : curl_test_https_tls12 ipv4 : nfqws2 " + good,
	}, "\n")
	res, err := scan.ParseBlockcheck(log)
	if err != nil {
		t.Fatal(err)
	}
	if res.TCPTLS != good {
		t.Fatalf("TCPTLS = %q, want %q", res.TCPTLS, good)
	}

	allBad := strings.Join([]string{
		"* SUMMARY",
		"curl_test_https_tls12 ipv4 example.com : nfqws2 --lua-init=onlybad",
		"* COVERAGE",
		"2/2 : curl_test_https_tls12 ipv4 : nfqws2 --lua-init=onlybad",
	}, "\n")
	res, err = scan.ParseBlockcheck(allBad)
	if err != nil {
		t.Fatal(err)
	}
	if res.TCPTLS != "" {
		t.Fatalf("TCPTLS = %q, want empty", res.TCPTLS)
	}
}
