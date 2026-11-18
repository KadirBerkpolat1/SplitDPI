package ops

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"os/exec"
	"os/signal"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"testing/fstest"
	"time"

	"github.com/KadirBerkpolat1/SplitDPI/internal/config"
	"github.com/KadirBerkpolat1/SplitDPI/internal/paths"
	"github.com/KadirBerkpolat1/SplitDPI/internal/scan"
	"github.com/KadirBerkpolat1/SplitDPI/internal/supervisor"
)

// Stop sends no SIGTERM to a scan, whose trials end with it, nor to this
// very process.
func TestStopSignalsNoScanNorSelf(t *testing.T) {
	shortStopWait(t, 0)
	p := paths.Under(t.TempDir())
	o := &Ops{P: p}

	sleeper := exec.Command("sleep", "30")
	if err := sleeper.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- sleeper.Wait() }()
	t.Cleanup(func() { sleeper.Process.Kill() })
	release := holdRunLock(t, p, "scan", sleeper.Process.Pid)
	if err := o.Stop(); err == nil || errors.Is(err, ErrStopTimeout) {
		t.Errorf("Stop during a scan: %v, want a refusal", err)
	}
	select {
	case err := <-exited:
		t.Fatalf("Stop signalled the scan: %v", err)
	case <-time.After(200 * time.Millisecond):
	}
	release()

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGTERM)
	defer signal.Stop(sigs)
	holdRunLock(t, p, "foreground", os.Getpid())
	if err := o.Stop(); err == nil || errors.Is(err, ErrStopTimeout) {
		t.Errorf("Stop of this process: %v, want a refusal", err)
	}
	select {
	case <-sigs:
		t.Fatal("Stop sent SIGTERM to its own process")
	case <-time.After(200 * time.Millisecond):
	}
}

// A status left by a run before, or by one restarting nfqws2, is not the new
// supervisor running.
func TestWaitRunningNeedsItsOwnStatus(t *testing.T) {
	shortWait(t)
	p := paths.Under(t.TempDir())
	o := &Ops{P: p}
	holdRunLock(t, p, "service", 22222)
	for _, stale := range []string{
		`{"state":"running","mode":"service","pid":11111}`,
		`{"state":"running","mode":"background","pid":22222}`,
		`{"state":"restarting","mode":"service","pid":22222}`,
	} {
		if err := os.WriteFile(p.StatusFile, []byte(stale), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, ok := o.waitRunning("service", nil); ok {
			t.Errorf("status %s passed for supervisor 22222 running", stale)
		}
	}
	if err := os.WriteFile(p.StatusFile, []byte(`{"state":"running","mode":"service","pid":22222}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if pid, ok := o.waitRunning("service", nil); !ok || pid != 22222 {
		t.Errorf("its own status: %d, %v", pid, ok)
	}
}

// A scan holds the run lock from start to end: a second scan, or a run, is
// refused meanwhile, and its trials still run.
func TestScanHoldsRunLock(t *testing.T) {
	o := siteScanOps(t)
	o.HTTP = &http.Client{Transport: &fakeHTTP{}}
	var once sync.Once
	var pid int
	var mode string
	var held bool
	var nested error
	progress := func(string) {
		once.Do(func() {
			pid, mode, held = supervisor.LockInfo(o.P)
			_, nested = o.Scan(context.Background(), scan.Options{ASN: 9121, Site: "pastebin.com"}, nil)
		})
	}
	rep, err := o.Scan(context.Background(), scan.Options{ASN: 9121, Site: "pastebin.com"}, progress)
	if err != nil || rep.Winner == nil {
		t.Fatalf("scan: %v, winner %+v", err, rep.Winner)
	}
	if !held || mode != "scan" || pid != os.Getpid() {
		t.Errorf("during blockcheck2 LockInfo = %d %q %v, want this scan's", pid, mode, held)
	}
	if !errors.As(nested, &RunLockError{}) {
		t.Errorf("a second scan during the first: %v, want RunLockError", nested)
	}
	if _, _, held := supervisor.LockInfo(o.P); held {
		t.Error("lock held after the scan")
	}
}

// Settings saved while a scan runs survive the strategy it saves.
func TestScanKeepsConfigSavedMeanwhile(t *testing.T) {
	o := siteScanOps(t)
	o.HTTP = &http.Client{Transport: &fakeHTTP{}}
	var once sync.Once
	var saveErr error
	progress := func(string) {
		once.Do(func() { saveErr = errors.Join(o.SetQNum(250), o.AppsEnable("youtube")) })
	}
	if _, err := o.Scan(context.Background(), scan.Options{ASN: 9121, Site: "pastebin.com"}, progress); err != nil {
		t.Fatal(err)
	}
	if saveErr != nil {
		t.Fatal(saveErr)
	}
	c, err := config.Load(o.P)
	if err != nil {
		t.Fatal(err)
	}
	if c.Strategy == nil || c.QNum != 250 || !slices.Contains(c.Apps, "youtube") {
		t.Errorf("after the scan: strategy %+v, qnum %d, apps %q", c.Strategy, c.QNum, c.Apps)
	}
}

// Changes saved at once, as the TUI does from its goroutines, all stay.
func TestConcurrentSavesKeepEveryChange(t *testing.T) {
	for run := range 30 {
		o := &Ops{P: paths.Under(t.TempDir())}
		errs := make([]error, 5)
		var wg sync.WaitGroup
		wg.Go(func() { errs[0] = o.AppsEnable("signal") })
		wg.Go(func() { errs[1] = o.AppsEnable("telegram") })
		wg.Go(func() { errs[2] = o.SetQNum(250) })
		wg.Go(func() { _, errs[3] = o.SitesAdd("a.example") })
		wg.Go(func() { _, errs[4] = o.ExcludeAdd("b.example") })
		wg.Wait()
		if err := errors.Join(errs...); err != nil {
			t.Fatal(err)
		}
		c, err := config.Load(o.P)
		if err != nil {
			t.Fatal(err)
		}
		sites, _ := o.SitesList()
		excl, _ := o.ExcludeList()
		if !slices.Contains(c.Apps, "signal") || !slices.Contains(c.Apps, "telegram") || c.QNum != 250 ||
			!slices.Equal(sites, []string{"a.example"}) || !slices.Equal(excl, []string{"b.example"}) {
			t.Fatalf("run %d lost a change: apps %q, qnum %d, sites %q, exclude %q", run, c.Apps, c.QNum, sites, excl)
		}
	}
}

// Each trial opens its own connections, through its own nfqws2: one reused
// from a trial before would pass a strategy that never handled the site.
func TestScanTrialsOpenNewConnections(t *testing.T) {
	o, _, _ := trialOps(t, fstest.MapFS{"proc/net/netfilter/nfnetlink_queue": {Data: []byte(queueBound)}})
	var conns atomic.Int32
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	srv.Config.ConnState = func(_ net.Conn, s http.ConnState) {
		if s == http.StateNew {
			conns.Add(1)
		}
	}
	srv.StartTLS()
	defer srv.Close()
	tr := srv.Client().Transport.(*http.Transport).Clone()
	tr.TLSClientConfig.InsecureSkipVerify = true
	tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		if addr != "open.example:443" {
			return nil, errors.New("connection reset by peer")
		}
		var d net.Dialer
		return d.DialContext(ctx, network, srv.Listener.Addr().String())
	}
	o.HTTP = &http.Client{Transport: tr}
	for _, d := range []string{"open.example", "shut.example"} {
		if _, err := o.SitesAdd(d); err != nil {
			t.Fatal(err)
		}
	}
	rep, err := o.Scan(context.Background(), scan.Options{ASN: 9121, NoBlockcheck: true}, nil)
	if !errors.Is(err, scan.ErrNoStrategy) || len(rep.Trials) < 2 {
		t.Fatalf("err %v, %d trials; want every trial to fail", err, len(rep.Trials))
	}
	if got := int(conns.Load()); got != len(rep.Trials) {
		t.Errorf("%d trials opened %d connections to open.example", len(rep.Trials), got)
	}
}

// noAddressSite opens pastebin.com and is noAddressHTTP for every other request.
type noAddressSite struct{}

func (noAddressSite) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Hostname() == "pastebin.com" {
		return (&fakeHTTP{}).RoundTrip(r)
	}
	return noAddressHTTP{}.RoundTrip(r)
}

// A site that has no address in any DNS is not tested by a trial, as probe
// does not test it: it cannot fail every strategy.
func TestScanSkipsSiteWithoutAddress(t *testing.T) {
	o, _, _ := trialOps(t, fstest.MapFS{"proc/net/netfilter/nfnetlink_queue": {Data: []byte(queueBound)}})
	o.HTTP = &http.Client{Transport: noAddressSite{}}
	old := lookupSystemDNS
	lookupSystemDNS = func(_ context.Context, name string) ([]netip.Addr, error) {
		return nil, &net.DNSError{Err: "no such host", Name: name, IsNotFound: true}
	}
	t.Cleanup(func() { lookupSystemDNS = old })
	for _, d := range []string{"pastebin.com", "nothing.example"} {
		if _, err := o.SitesAdd(d); err != nil {
			t.Fatal(err)
		}
	}
	rep, err := o.Scan(context.Background(), scan.Options{ASN: 9121, NoBlockcheck: true}, nil)
	if err != nil || rep.Winner == nil {
		t.Fatalf("err %v, trials %+v", err, rep.Trials)
	}
	if rs := rep.Trials[0].Results; len(rs) != 2 || !rs[1].NoAddress {
		t.Errorf("results %+v, want nothing.example without address", rs)
	}
}

// The country a scan ran for picks the exclude list, in its trials and in
// the runs of the strategy it saves, also with no ISP preset to tell it.
func TestScanKeepsCountryExcludes(t *testing.T) {
	o := siteScanOps(t)
	o.HTTP = &http.Client{Transport: &fakeHTTP{}}
	var trialExclude []byte
	spawn := o.Spawn
	o.Spawn = func(argv []string) (supervisor.Child, error) {
		trialExclude, _ = os.ReadFile(o.P.ExcludeFile)
		return spawn(argv)
	}
	if _, err := o.Scan(context.Background(), scan.Options{Country: "RU", Site: "pastebin.com"}, nil); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(fileLines(trialExclude), "gosuslugi.ru") {
		t.Error("the trial ran without exclude/ru.txt")
	}
	c, err := config.Load(o.P)
	if err != nil {
		t.Fatal(err)
	}
	j, err := o.prepare(c, "foreground", io.Discard, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(j.out.Exclude, "gosuslugi.ru") {
		t.Errorf("a run of the saved strategy %+v leaves out exclude/ru.txt", c.Strategy)
	}
}

// A generic strategy picked by hand after a scan keeps the network's country,
// which no ISP preset names.
func TestStrategySetKeepsCountry(t *testing.T) {
	o := siteScanOps(t)
	o.HTTP = &http.Client{Transport: &fakeHTTP{}}
	if _, err := o.Scan(context.Background(), scan.Options{ASN: 12389, Country: "RU", Site: "pastebin.com"}, nil); err != nil {
		t.Fatal(err)
	}
	set := o.PresetsList()
	choices := strategyChoices(set)
	i := slices.IndexFunc(choices, func(ch StrategyChoice) bool { return ch.Country == "" })
	if _, _, err := o.StrategySet(choices[i].Strategy.ID); err != nil {
		t.Fatal(err)
	}
	c, err := config.Load(o.P)
	if err != nil {
		t.Fatal(err)
	}
	if got := countryFor(set, c.Strategy); got != "RU" {
		t.Errorf("country %q, want RU; strategy %+v", got, c.Strategy)
	}
}

// A supervisor restarting nfqws2 is still on while it holds the lock; once
// nothing holds it, its last status is stale.
func TestStatusRestarting(t *testing.T) {
	p := paths.Under(t.TempDir())
	o := &Ops{P: p}
	release := holdRunLock(t, p, "service", 22222)
	if err := os.WriteFile(p.StatusFile, []byte(`{"state":"restarting","mode":"service","pid":22222}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if st, _, _ := o.Status(); st.State != "restarting" {
		t.Errorf("lock held: state %q", st.State)
	}
	release()
	if st, _, _ := o.Status(); st.State != "stopped" {
		t.Errorf("lock free: state %q", st.State)
	}
}

func fileLines(b []byte) []string {
	var out []string
	for l := range strings.Lines(string(b)) {
		out = append(out, strings.TrimSuffix(l, "\n"))
	}
	return out
}
