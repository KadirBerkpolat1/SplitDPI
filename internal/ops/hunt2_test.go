package ops

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/KadirBerkpolat1/SplitDPI/internal/config"
	"github.com/KadirBerkpolat1/SplitDPI/internal/dns"
	"github.com/KadirBerkpolat1/SplitDPI/internal/paths"
	"github.com/KadirBerkpolat1/SplitDPI/internal/presets"
	"github.com/KadirBerkpolat1/SplitDPI/internal/probe"
	"github.com/KadirBerkpolat1/SplitDPI/internal/scan"
	"github.com/KadirBerkpolat1/SplitDPI/internal/supervisor"
	"github.com/KadirBerkpolat1/SplitDPI/internal/sysinfo"
)

// runService runs o as the service until the test ends, with a strategy saved.
func runService(t *testing.T, o *Ops) {
	t.Helper()
	cfg := trialConfig()
	cfg.Strategy = &config.Strategy{ID: "tt", TCPTLS: candidate.TCPTLS}
	if err := config.Save(o.P, cfg); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- o.Run(ctx, true) }()
	t.Cleanup(func() { cancel(); <-done })
}

// nfqws2 dying while it starts, before it binds the queue, never reads as
// running: waitRunning does not take its crash loop for a start.
func TestCrashingStartNeverRunning(t *testing.T) {
	shortWait(t)
	enableWait = 400 * time.Millisecond
	o, _, _ := trialOps(t, fstest.MapFS{})
	o.Spawn = func([]string) (supervisor.Child, error) {
		c := &child{exit: make(chan error, 1)}
		time.AfterFunc(60*time.Millisecond, func() {
			select {
			case c.exit <- errors.New("exit status 1"):
			default:
			}
		})
		return c, nil
	}
	runService(t, o)
	if pid, ok := o.waitRunning("service", nil); ok {
		t.Errorf("nfqws2 dying 60 ms after its start passed as running (pid %d)", pid)
	}
}

// The service reads as running once its nfqws2 has bound the queue, and only
// a binding of its own counts.
func TestRunningOnceQueueBound(t *testing.T) {
	for _, tc := range []struct {
		name, queue string
		want        bool
	}{
		{"bound by another process", queueBound, false},
		{"bound by nfqws2", "  200   4242     0 2 65531     0     0        0  1\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			shortWait(t)
			enableWait = 400 * time.Millisecond
			o, _, _ := trialOps(t, fstest.MapFS{"proc/net/netfilter/nfnetlink_queue": {Data: []byte(tc.queue)}})
			runService(t, o)
			pid, ok := o.waitRunning("service", nil)
			if ok != tc.want || ok && pid != os.Getpid() {
				t.Errorf("waitRunning = %d, %v; want %v", pid, ok, tc.want)
			}
		})
	}
}

// Status takes what runs from the lock: a scan holding it is running in mode
// scan, and an on status of another process or mode is stale.
func TestStatusFollowsLockHolder(t *testing.T) {
	for _, tc := range []struct {
		name, lockMode string
		lockPID        int
		status         string
		want           supervisor.Status
	}{
		{"dead service under a scan", "scan", 33333, `{"state":"running","mode":"service","pid":11111}`,
			supervisor.Status{State: "running", Mode: "scan", PID: 33333}},
		{"scan between trials", "scan", 33333, `{"state":"stopped","mode":"scan","pid":33333}`,
			supervisor.Status{State: "running", Mode: "scan", PID: 33333}},
		{"dead service under a new one", "service", 22222, `{"state":"running","mode":"service","pid":11111}`,
			supervisor.Status{State: "stopped", Mode: "service", PID: 11111}},
		{"background status under the service", "service", 22222, `{"state":"restarting","mode":"background","pid":22222}`,
			supervisor.Status{State: "stopped", Mode: "background", PID: 22222}},
		{"its own", "service", 22222, `{"state":"running","mode":"service","pid":22222}`,
			supervisor.Status{State: "running", Mode: "service", PID: 22222}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := paths.Under(t.TempDir())
			o := &Ops{P: p}
			holdRunLock(t, p, tc.lockMode, tc.lockPID)
			if err := os.WriteFile(p.StatusFile, []byte(tc.status), 0o644); err != nil {
				t.Fatal(err)
			}
			st, _, _ := o.Status()
			if st.State != tc.want.State || st.Mode != tc.want.Mode || st.PID != tc.want.PID {
				t.Errorf("Status = %s %s %d, want %s %s %d", st.State, st.Mode, st.PID, tc.want.State, tc.want.Mode, tc.want.PID)
			}
			if tc.lockMode == "scan" && st.Since.IsZero() {
				t.Error("a scan's status has no start time")
			}
		})
	}
}

// recordBlockcheck makes o's commands, nft's included, also land in calls.
func recordBlockcheck(o *Ops) *[]string {
	var calls []string
	var mu sync.Mutex
	run := o.Exec
	o.Exec = func(ctx context.Context, stdin, name string, args ...string) ([]byte, error) {
		mu.Lock()
		calls = append(calls, strings.Join(append([]string{name}, args...), " "))
		mu.Unlock()
		return run(ctx, stdin, name, args...)
	}
	return &calls
}

// A package question cancelled (Ctrl+C, Esc) ends the scan with ctx's error:
// no package error, no install, no blockcheck2.
func TestScanPackagesCancelled(t *testing.T) {
	o := siteScanOps(t)
	osr, err := os.ReadFile("../sysinfo/testdata/os-release/debian13")
	if err != nil {
		t.Fatal(err)
	}
	o.Root.(fstest.MapFS)["etc/os-release"] = &fstest.MapFile{Data: osr}
	m := newMachine("curl")
	o.LookPath = m.lookPath
	nft := o.Exec
	o.Exec = func(ctx context.Context, stdin, name string, args ...string) ([]byte, error) {
		if name == "nft" {
			return nft(ctx, stdin, name, args...)
		}
		return m.run(ctx, stdin, name, args...)
	}
	o.HTTP = &http.Client{Transport: &fakeHTTP{}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var log []string
	opt := scan.Options{ASN: 9121, Site: "pastebin.com", Packages: func(ctx context.Context, _ sysinfo.Packages) (bool, error) {
		cancel()
		return false, ctx.Err()
	}}
	_, err = o.Scan(ctx, opt, func(l string) { log = append(log, l) })
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err %v, want context.Canceled", err)
	}
	if slices.ContainsFunc(m.calls, func(c string) bool { return strings.HasPrefix(c, "apt-get") }) ||
		slices.ContainsFunc(log, func(l string) bool { return strings.HasPrefix(l, "env DOMAINS=") }) {
		t.Errorf("ran %q; log %q", m.calls, log)
	}
}

// A DNS question cancelled ends the scan with ctx's error, and blockcheck2
// never starts.
func TestScanDNSCancelled(t *testing.T) {
	o, _ := dnsScanOps(t)
	fakeSystemDNS(t, "195.175.254.2")
	if _, err := o.SetDNS("off", dns.DoT); err != nil {
		t.Fatal(err)
	}
	calls := recordBlockcheck(o)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	asked := false
	opt := scan.Options{ASN: 9121, Site: "pastebin.com", DNSCheck: func(ctx context.Context, _ []probe.DNSResult) (string, error) {
		asked = true
		cancel()
		return "", ctx.Err()
	}}
	_, err := o.Scan(ctx, opt, nil)
	if !asked || !errors.Is(err, context.Canceled) {
		t.Errorf("asked %v, err %v; want context.Canceled", asked, err)
	}
	if slices.ContainsFunc(*calls, func(c string) bool { return strings.Contains(c, "blockcheck2") }) {
		t.Errorf("blockcheck2 started: %q", *calls)
	}
}

// Each probe opens its own connection: one kept from a probe before would
// pass through no DPI and show a blocked site open.
func TestProbeOpensNewConnections(t *testing.T) {
	var conns atomic.Int32
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) }))
	srv.Config.ConnState = func(_ net.Conn, s http.ConnState) {
		if s == http.StateNew {
			conns.Add(1)
		}
	}
	srv.StartTLS()
	defer srv.Close()
	tr := srv.Client().Transport.(*http.Transport).Clone()
	tr.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, network, srv.Listener.Addr().String())
	}
	o := &Ops{P: paths.Under(t.TempDir()), HTTP: &http.Client{Transport: tr}}
	if err := o.AppNew("mine", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := o.AppHostAdd("mine", "example.com"); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		res, err := o.Probe(context.Background(), []string{"mine"})
		if err != nil || len(res["mine"]) != 1 || !res["mine"][0].OK {
			t.Fatalf("probe %+v, %v", res, err)
		}
	}
	if n := conns.Load(); n != 2 {
		t.Errorf("2 probes opened %d connections, want 2", n)
	}
}

// A country's strategy for another country than the network's sets that
// country and drops the old ASN, which selected the old country's excludes.
func TestStrategySetOtherCountryStrategy(t *testing.T) {
	o := strategyOps(t)
	if err := os.CopyFS(o.P.PresetCache, presets.Embedded()); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(filepath.Join(o.P.PresetCache, "countries", "ru.toml"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("\n[[strategy]]\nid = \"ru-country-split\"\ntcp_tls = \"--payload=tls_client_hello --lua-desync=multisplit:pos=2\"\n")
	f.Close()
	for _, tc := range []struct {
		id      string
		country string
		asn     int
	}{
		{"ru-country-split", "RU", 0},
		{"generic-multidisorder", "TR", 9121},
	} {
		c := config.Default()
		c.Strategy = &config.Strategy{ID: "tt-multidisorder", Source: "isp/tr/as9121-turk-telekom.toml", TCPTLS: "--x", ASN: 9121, Country: "TR"}
		if err := config.Save(o.P, c); err != nil {
			t.Fatal(err)
		}
		if _, _, err := o.StrategySet(tc.id); err != nil {
			t.Fatal(err)
		}
		got, _ := config.Load(o.P)
		if got.Strategy == nil || got.Strategy.Country != tc.country || got.Strategy.ASN != tc.asn {
			t.Errorf("%s: saved %+v, want country %s, ASN %d", tc.id, got.Strategy, tc.country, tc.asn)
		}
	}
}

// Concurrent edits of one app keep every address added.
func TestConcurrentAppHostAdd(t *testing.T) {
	o := &Ops{P: paths.Under(t.TempDir())}
	if err := o.AppNew("mine", ""); err != nil {
		t.Fatal(err)
	}
	for i := range 50 {
		add := []string{fmt.Sprintf("a%d.example", i), fmt.Sprintf("b%d.example", i)}
		var wg sync.WaitGroup
		for _, d := range add {
			wg.Go(func() {
				if _, err := o.AppHostAdd("mine", d); err != nil {
					t.Error(err)
				}
			})
		}
		wg.Wait()
		a, err := o.App("mine")
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range add {
			if !slices.Contains(a.Hosts, d) {
				t.Fatalf("round %d lost %s: hosts %q", i, d, a.Hosts)
			}
		}
	}
}
