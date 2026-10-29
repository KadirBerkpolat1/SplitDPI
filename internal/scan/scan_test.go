package scan_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/KadirBerkpolat1/SplitDPI/internal/engine"
	"github.com/KadirBerkpolat1/SplitDPI/internal/presets"
	"github.com/KadirBerkpolat1/SplitDPI/internal/probe"
	"github.com/KadirBerkpolat1/SplitDPI/internal/scan"
)

func testSet() presets.Set {
	return presets.Set{
		Apps: map[string]presets.App{
			"roblox": {
				ID:    "roblox",
				Probe: []string{"https://www.roblox.com/"},
			},
			"discord": {
				ID:    "discord",
				Probe: []string{"https://discord.com/api/v9/gateway"},
				UDP:   &presets.UDP{Ports: "50000-50100"},
			},
			"youtube": {ID: "youtube", Probe: []string{"https://www.youtube.com/"}, QUIC: true},
		},
		Generic: []presets.Strategy{
			{ID: "fail-one", TCPTLS: "--payload=tls_client_hello --lua-desync=drop"},
			{ID: "win-two", TCPTLS: "--payload=tls_client_hello --lua-desync=fake"},
		},
	}
}

func testEnv(t *testing.T, set presets.Set, probeFn func(id string) []probe.Result) (scan.Env, *atomic.Int32, *atomic.Int32) {
	t.Helper()
	var trials, blockchecks atomic.Int32
	var current string
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	env := scan.Env{
		Set: set,
		Lookup: func(context.Context) (scan.ASNInfo, error) {
			return scan.ASNInfo{ASN: 1, Country: "XX", Org: "test"}, nil
		},
		StartTrial: func(_ context.Context, st presets.Strategy) (func(), error) {
			trials.Add(1)
			current = st.ID
			return func() {}, nil
		},
		Probe: func(_ context.Context, _ []string) []probe.Result {
			return probeFn(current)
		},
		Blockcheck: func(context.Context, scan.BlockcheckParams, func(string)) (string, error) {
			blockchecks.Add(1)
			return readFixture(t, "bc-simulate.log"), nil
		},
		Now:       func() time.Time { return now },
		EngineVer: engine.Version,
	}
	return env, &trials, &blockchecks
}

// A trial that cannot apply its bypass must not be probed: that would test no bypass.
func TestScanTrialErrorStops(t *testing.T) {
	env, _, bc := testEnv(t, testSet(), func(string) []probe.Result {
		t.Fatal("probe must not run without a trial bypass")
		return nil
	})
	boom := errors.New("nfqws2 did not bind queue 200")
	env.StartTrial = func(context.Context, presets.Strategy) (func(), error) { return nil, boom }
	rep, err := scan.Scan(context.Background(), env, scan.Options{Apps: []string{"roblox"}})
	if !errors.Is(err, boom) || rep.Winner != nil {
		t.Fatalf("got %v, winner %+v", err, rep.Winner)
	}
	if bc.Load() != 0 {
		t.Fatalf("blockcheck calls %d", bc.Load())
	}
}

func TestScanSecondCandidateWins(t *testing.T) {
	env, trials, bc := testEnv(t, testSet(), func(id string) []probe.Result {
		ok := id == "win-two"
		return []probe.Result{{URL: "https://www.roblox.com/", OK: ok}}
	})
	rep, err := scan.Scan(context.Background(), env, scan.Options{Apps: []string{"roblox"}})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Winner == nil || rep.Winner.ID != "win-two" || rep.Winner.Source != "generic.toml" {
		t.Fatalf("winner %+v", rep.Winner)
	}
	if bc.Load() != 0 {
		t.Fatalf("blockcheck calls %d", bc.Load())
	}
	if trials.Load() != 2 {
		t.Fatalf("trials %d", trials.Load())
	}
}

// blockcheckOK passes only the trials of blockcheck2's strategies.
func blockcheckOK(id string) []probe.Result {
	return []probe.Result{{OK: strings.HasPrefix(id, "blockcheck-")}}
}

func TestScanBlockcheckFallback(t *testing.T) {
	env, _, bc := testEnv(t, testSet(), blockcheckOK)
	rep, err := scan.Scan(context.Background(), env, scan.Options{Apps: []string{"roblox"}})
	if err != nil {
		t.Fatal(err)
	}
	if bc.Load() != 1 {
		t.Fatalf("blockcheck calls %d", bc.Load())
	}
	if rep.Winner == nil || rep.Winner.Source != "blockcheck2" {
		t.Fatalf("winner %+v", rep.Winner)
	}
	if rep.Winner.TCPTLS != wantSimulateTLS || rep.Winner.QUIC != wantSimulateQUIC {
		t.Fatalf("winner %q / %q", rep.Winner.TCPTLS, rep.Winner.QUIC)
	}
	if n := len(rep.Trials); n != 3 || rep.Trials[2].Candidate.Source != "blockcheck2" || !rep.Trials[2].OK {
		t.Fatalf("trials %+v, want 2 preset trials and 1 passing blockcheck2 trial", rep.Trials)
	}
}

// Options.Trial sees every trial as it ends; it replaces the blockcheck2
// trial progress line.
func TestScanTrialCallback(t *testing.T) {
	env, _, _ := testEnv(t, testSet(), blockcheckOK)
	var lines []string
	env.Progress = func(l string) { lines = append(lines, l) }
	var seen []string
	opt := scan.Options{Apps: []string{"roblox"}, Trial: func(tr scan.Trial) { seen = append(seen, tr.Candidate.Strategy.ID) }}
	rep, err := scan.Scan(context.Background(), env, opt)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"fail-one", "win-two", "blockcheck-1"}; len(rep.Trials) != 3 || !slices.Equal(seen, want) {
		t.Fatalf("trial callbacks %q, want %q", seen, want)
	}
	for _, l := range lines {
		if strings.Contains(l, "blockcheck-1") {
			t.Errorf("progress line %q duplicates the trial callback", l)
		}
	}
}

func TestScanNoBlockcheckAllFail(t *testing.T) {
	set := testSet()
	env, _, bc := testEnv(t, set, func(string) []probe.Result {
		return []probe.Result{{OK: false}}
	})
	rep, err := scan.Scan(context.Background(), env, scan.Options{Apps: []string{"roblox"}, NoBlockcheck: true})
	if !errors.Is(err, scan.ErrNoStrategy) {
		t.Fatalf("err %v", err)
	}
	if len(rep.Trials) != len(set.Candidates(1, "XX")) {
		t.Fatalf("trials %d want %d", len(rep.Trials), len(set.Candidates(1, "XX")))
	}
	if bc.Load() != 0 {
		t.Fatalf("blockcheck called")
	}
}

func TestScanBlockcheckOnly(t *testing.T) {
	env, trials, bc := testEnv(t, testSet(), func(id string) []probe.Result {
		if !strings.HasPrefix(id, "blockcheck-") {
			t.Fatalf("preset %s probed with --blockcheck-only", id)
		}
		return []probe.Result{{OK: true}}
	})
	rep, err := scan.Scan(context.Background(), env, scan.Options{Apps: []string{"roblox"}, BlockcheckOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if trials.Load() != 1 {
		t.Fatalf("trials %d, want only the blockcheck2 one", trials.Load())
	}
	if bc.Load() != 1 || rep.Winner.Source != "blockcheck2" {
		t.Fatalf("bc %d winner %+v", bc.Load(), rep.Winner)
	}
}

func TestScanVoiceUnverified(t *testing.T) {
	env, _, _ := testEnv(t, testSet(), func(id string) []probe.Result {
		return []probe.Result{{OK: id == "win-two"}}
	})
	rep, err := scan.Scan(context.Background(), env, scan.Options{Apps: []string{"discord", "roblox"}})
	if err != nil {
		t.Fatal(err)
	}
	if !rep.VoiceUnverified {
		t.Fatal("want voice unverified")
	}
}

func TestScanWinnerProvenance(t *testing.T) {
	env, _, _ := testEnv(t, testSet(), func(id string) []probe.Result {
		return []probe.Result{{OK: id == "win-two"}}
	})
	rep, _ := scan.Scan(context.Background(), env, scan.Options{Apps: []string{"roblox"}})
	w := rep.Winner
	if w.ASN != 1 || w.Date != "2026-10-28" || w.Engine != engine.Version {
		t.Fatalf("provenance %+v", w)
	}
}

// A strategy only wins if the user's sites open too, and blockcheck2 tests them.
func TestScanProbesSites(t *testing.T) {
	env, _, _ := testEnv(t, testSet(), nil)
	var probed [][]string
	blockchecked := false
	env.Probe = func(_ context.Context, urls []string) []probe.Result {
		probed = append(probed, urls)
		rs := make([]probe.Result, len(urls))
		for i, u := range urls {
			rs[i] = probe.Result{URL: u, OK: blockchecked || u != "https://pastebin.com/"}
		}
		return rs
	}
	var domains []string
	env.Blockcheck = func(_ context.Context, p scan.BlockcheckParams, _ func(string)) (string, error) {
		domains, blockchecked = p.Domains, true
		return readFixture(t, "bc-simulate.log"), nil
	}
	rep, err := scan.Scan(context.Background(), env, scan.Options{Apps: []string{"roblox"}, Sites: []string{"pastebin.com", "www.roblox.com"}})
	if err != nil {
		t.Fatal(err)
	}
	wantURLs := []string{"https://www.roblox.com/", "https://pastebin.com/", "https://www.roblox.com/"}
	if len(probed) != 3 || !slices.Equal(probed[0], wantURLs) || !slices.Equal(probed[2], wantURLs) {
		t.Fatalf("probed %q, want each preset and blockcheck2 trial to probe %q", probed, wantURLs)
	}
	if rep.Winner == nil || rep.Winner.Source != "blockcheck2" {
		t.Fatalf("a trial won although the site failed: %+v", rep.Winner)
	}
	if want := []string{"www.roblox.com", "pastebin.com"}; !slices.Equal(domains, want) {
		t.Errorf("blockcheck domains %q, want %q", domains, want)
	}
}

// A site scan skips the preset trials and gives blockcheck2 exactly that domain,
// over IPv4 and IPv6 only when the machine has IPv6.
func TestScanOneSite(t *testing.T) {
	for _, ipv6 := range []bool{false, true} {
		env, trials, _ := testEnv(t, testSet(), nil)
		var probed []string
		env.Probe = func(_ context.Context, urls []string) []probe.Result {
			probed = urls
			return []probe.Result{{OK: true}}
		}
		env.IPv6 = ipv6
		var got scan.BlockcheckParams
		env.Blockcheck = func(_ context.Context, p scan.BlockcheckParams, _ func(string)) (string, error) {
			got = p
			return readFixture(t, "bc-simulate.log"), nil
		}
		rep, err := scan.Scan(context.Background(), env, scan.Options{Apps: []string{"roblox"}, Sites: []string{"example.org"}, Site: "pastebin.com"})
		if err != nil {
			t.Fatal(err)
		}
		want := "4"
		if ipv6 {
			want = "46"
		}
		if !slices.Equal(got.Domains, []string{"pastebin.com"}) || got.IPVS != want {
			t.Errorf("ipv6 %v: params %+v", ipv6, got)
		}
		// The only trial is blockcheck2's, probing the apps, the sites and the scanned site.
		wantURLs := []string{"https://www.roblox.com/", "https://example.org/", "https://pastebin.com/"}
		if trials.Load() != 1 || rep.Trials[0].Candidate.Source != "blockcheck2" || !slices.Equal(probed, wantURLs) {
			t.Errorf("ipv6 %v: trials %+v, probed %q", ipv6, rep.Trials, probed)
		}
		if rep.Winner == nil || rep.Winner.Source != "blockcheck2" {
			t.Errorf("winner %+v", rep.Winner)
		}
	}
	env, _, _ := testEnv(t, testSet(), blockcheckOK)
	if _, err := scan.Scan(context.Background(), env, scan.Options{Site: "pastebin.com"}); err != nil {
		t.Errorf("site scan without apps: %v", err)
	}
}

func TestScanSitesWithoutApps(t *testing.T) {
	env, _, _ := testEnv(t, testSet(), func(id string) []probe.Result {
		return []probe.Result{{OK: id == "fail-one"}}
	})
	rep, err := scan.Scan(context.Background(), env, scan.Options{Sites: []string{"pastebin.com"}})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Winner == nil || rep.Winner.ID != "fail-one" {
		t.Fatalf("winner %+v", rep.Winner)
	}
}
