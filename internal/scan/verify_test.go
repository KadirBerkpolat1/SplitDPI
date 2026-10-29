package scan_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/KadirBerkpolat1/SplitDPI/internal/presets"
	"github.com/KadirBerkpolat1/SplitDPI/internal/probe"
	"github.com/KadirBerkpolat1/SplitDPI/internal/scan"
)

func TestTLSCandidatesCoverageOrder(t *testing.T) {
	res, err := scan.ParseBlockcheck(strings.Join([]string{
		"* SUMMARY",
		"curl_test_https_tls12 ipv4 a.com : nfqws2 " + tlsSimple,
		"curl_test_https_tls12 ipv4 b.com : nfqws2 " + tlsDisord,
		"curl_test_https_tls12 ipv6 a.com : nfqws2 " + tlsDisord,
		"curl_test_https_tls12 ipv4 a.com : nfqws2 --lua-init=x --payload=tls_client_hello --lua-desync=drop",
		"",
		"* COVERAGE",
		"2/2 : curl_test_https_tls12 ipv4 : nfqws2 " + tlsDisord,
		"2/2 : curl_test_https_tls12 ipv4 : nfqws2 --lua-init=x --payload=tls_client_hello --lua-desync=drop",
		"1/2 : curl_test_https_tls12 ipv6 : nfqws2 " + tlsDisord,
		"1/2 : curl_test_https_tls12 ipv4 : nfqws2 " + tlsSimple,
	}, "\n"))
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{tlsDisord, tlsSimple}; !slices.Equal(res.TLSCandidates, want) {
		t.Errorf("candidates %q, want %q", res.TLSCandidates, want)
	}
}

// Without COVERAGE: strategies working on IPv4 and IPv6 first, then the rest
// in SUMMARY order; duplicates and lines with Lua code are dropped.
func TestTLSCandidatesSummaryOrder(t *testing.T) {
	const ipv6Only = "--payload=tls_client_hello --lua-desync=multisplit:pos=2"
	res, err := scan.ParseBlockcheck(siteLog(
		"curl_test_https_tls12 ipv4 pastebin.com : nfqws2 "+tlsSimple,
		"curl_test_https_tls12 ipv4 pastebin.com : nfqws2 --payload=tls_client_hello --lua-desync=luaexec:code=x --lua-desync=drop",
		"curl_test_https_tls12 ipv4 pastebin.com : nfqws2 "+tlsDisord,
		"curl_test_https_tls12 ipv4 pastebin.com : nfqws2 "+tlsSimple,
		"curl_test_https_tls12 ipv6 pastebin.com : nfqws2 "+tlsDisord,
		"curl_test_https_tls12 ipv6 pastebin.com : nfqws2 "+ipv6Only,
	))
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{tlsDisord, tlsSimple, ipv6Only}; !slices.Equal(res.TLSCandidates, want) {
		t.Errorf("candidates %q, want %q", res.TLSCandidates, want)
	}
	if res.TCPTLS != tlsDisord {
		t.Errorf("TCPTLS %q", res.TCPTLS)
	}
}

// verifyEnv runs a blockcheck-only scan of log; passes decides each trial by its TLS strategy.
func verifyEnv(t *testing.T, log string, passes func(tls string) bool) (scan.Env, *[]string) {
	t.Helper()
	env, _, _ := testEnv(t, testSet(), nil)
	var tried []string
	var current string
	env.StartTrial = func(_ context.Context, st presets.Strategy) (func(), error) {
		current = st.TCPTLS
		tried = append(tried, st.TCPTLS)
		return func() {}, nil
	}
	env.Probe = func(_ context.Context, urls []string) []probe.Result {
		return []probe.Result{{URL: urls[0], OK: passes(current)}}
	}
	env.Blockcheck = func(context.Context, scan.BlockcheckParams, func(string)) (string, error) { return log, nil }
	return env, &tried
}

func TestBlockcheckFirstPassingWins(t *testing.T) {
	log := siteLog(
		"curl_test_https_tls12 ipv4 pastebin.com : nfqws2 "+tlsSimple,
		"curl_test_https_tls12 ipv4 pastebin.com : nfqws2 "+tlsDisord,
		"curl_test_http3 ipv4 pastebin.com : nfqws2 "+quicSimple,
	)
	env, tried := verifyEnv(t, log, func(tls string) bool { return tls == tlsDisord })
	rep, err := scan.Scan(context.Background(), env, scan.Options{Apps: []string{"roblox"}, BlockcheckOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Winner == nil || rep.Winner.TCPTLS != tlsDisord || rep.Winner.QUIC != quicSimple {
		t.Fatalf("winner %+v", rep.Winner)
	}
	if !slices.Equal(*tried, []string{tlsSimple, tlsDisord}) || len(rep.Trials) != 2 || rep.Trials[0].OK || !rep.Trials[1].OK {
		t.Errorf("tried %q, trials %+v", *tried, rep.Trials)
	}
}

func TestBlockcheckNonePassing(t *testing.T) {
	log := siteLog("curl_test_https_tls12 ipv4 pastebin.com : nfqws2 " + tlsSimple)
	env, _ := verifyEnv(t, log, func(string) bool { return false })
	rep, err := scan.Scan(context.Background(), env, scan.Options{Site: "pastebin.com"})
	if !errors.Is(err, scan.ErrNoStrategy) || rep.Winner != nil {
		t.Fatalf("err %v, winner %+v", err, rep.Winner)
	}
	if len(rep.Trials) != 1 || rep.Trials[0].OK {
		t.Errorf("trials %+v", rep.Trials)
	}
}

func TestBlockcheckTrialsCapped(t *testing.T) {
	var lines []string
	for i := range scan.MaxBlockcheckTrials + 5 {
		lines = append(lines, fmt.Sprintf("curl_test_https_tls12 ipv4 pastebin.com : nfqws2 --payload=tls_client_hello --lua-desync=multisplit:pos=%d", i+1))
	}
	env, tried := verifyEnv(t, siteLog(lines...), func(string) bool { return false })
	if _, err := scan.Scan(context.Background(), env, scan.Options{Site: "pastebin.com"}); !errors.Is(err, scan.ErrNoStrategy) {
		t.Fatalf("err %v", err)
	}
	if len(*tried) != scan.MaxBlockcheckTrials {
		t.Errorf("%d trials, want the cap %d", len(*tried), scan.MaxBlockcheckTrials)
	}
}

// On the real Türk Telekom run, blockcheck2's first line (tcpseg) opened
// discord.com but broke the other probes; only multidisorder:pos=2:seqovl=1
// passed them all.
func TestBlockcheckRealFixtureRetested(t *testing.T) {
	const good = "--payload=tls_client_hello --lua-desync=multidisorder:pos=2:seqovl=1"
	env, tried := verifyEnv(t, readFixture(t, "bc-real-tr.log"), func(tls string) bool { return tls == good })
	rep, err := scan.Scan(context.Background(), env, scan.Options{Site: "discord.com"})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Winner == nil || rep.Winner.TCPTLS != good {
		t.Fatalf("winner %+v", rep.Winner)
	}
	if !strings.Contains((*tried)[0], "tcpseg:pos=0,1") {
		t.Errorf("first trial %q, want blockcheck2's first line", (*tried)[0])
	}
	for _, tls := range *tried {
		if presets.ValidateArgs(tls) != nil {
			t.Errorf("tried a strategy with Lua code: %q", tls)
		}
	}
}
