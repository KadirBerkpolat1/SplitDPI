package ops

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"syscall"
	"testing"
	"testing/fstest"

	"github.com/KadirBerkpolat1/SplitDPI/internal/config"
	"github.com/KadirBerkpolat1/SplitDPI/internal/paths"
	"github.com/KadirBerkpolat1/SplitDPI/internal/scan"
)

func TestScanRefusesWhileRunning(t *testing.T) {
	o := &Ops{
		P:    paths.Under(t.TempDir()),
		Root: fstest.MapFS{},
		Exec: func(context.Context, string, string, ...string) ([]byte, error) { return nil, nil },
	}
	if err := os.MkdirAll(o.P.RunDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(o.P.LockFile, []byte(`{"pid":999999,"mode":"service"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	// A stale lock file (kill -9) does not block a scan.
	if _, err := o.Scan(context.Background(), scan.Options{}, nil); errors.As(err, &RunLockError{}) {
		t.Fatalf("stale lock blocked scan: %v", err)
	}

	f, err := os.Open(o.P.LockFile)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	if _, err := o.Scan(context.Background(), scan.Options{}, nil); !errors.As(err, &RunLockError{}) {
		t.Fatalf("got %v, want RunLockError", err)
	}
}

// A site scan runs blockcheck2 on that one domain only, saves the strategy and
// adds the site to the user sites. The engine here is a fake blockcheck2.sh.
const siteTLS = "--payload=tls_client_hello --lua-desync=multisplit:pos=1"

// siteScanOps is trialOps with a fake blockcheck2.sh that reports siteTLS working.
func siteScanOps(t *testing.T) *Ops {
	t.Helper()
	o, _, _ := trialOps(t, fstest.MapFS{"proc/net/netfilter/nfnetlink_queue": {Data: []byte(queueBound)}})
	script := "#!/bin/sh\necho \"env DOMAINS=$DOMAINS IPVS=$IPVS\"\necho\necho '* SUMMARY'\n" +
		"echo \"curl_test_https_tls12 ipv4 $DOMAINS : nfqws2 " + siteTLS + "\"\n"
	if err := os.WriteFile(filepath.Join(o.P.EngineDir, "zapret2-v1.0.6", "blockcheck2.sh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return o
}

func TestScanSite(t *testing.T) {
	o := siteScanOps(t)
	h := &fakeHTTP{}
	o.HTTP = &http.Client{Transport: h}
	const tls = siteTLS
	if _, err := o.Scan(context.Background(), scan.Options{ASN: 9121, Site: "not a domain"}, nil); err == nil {
		t.Fatal("bad site accepted")
	}
	var log []string
	rep, err := o.Scan(context.Background(), scan.Options{ASN: 9121, Site: "https://PasteBin.com/x"}, func(l string) { log = append(log, l) })
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(log, "env DOMAINS=pastebin.com IPVS=4") {
		t.Errorf("blockcheck2 saw %q", log)
	}
	if len(rep.Trials) != 1 || !rep.Trials[0].OK || !slices.Equal(h.seen(), []string{"https://pastebin.com/"}) {
		t.Errorf("trials %+v, probed %q", rep.Trials, h.seen())
	}
	c, _ := config.Load(o.P)
	if c.Strategy == nil || c.Strategy.TCPTLS != tls || c.Strategy.Source != "blockcheck2" {
		t.Errorf("saved %+v", c.Strategy)
	}
	if sites, _ := o.SitesList(); !slices.Equal(sites, []string{"pastebin.com"}) {
		t.Errorf("sites %q", sites)
	}
}

type failHTTP struct{}

func (failHTTP) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("connection reset by peer")
}

// A blockcheck2 strategy that fails the re-test is not saved, and the site is not added.
func TestScanSiteNonePassing(t *testing.T) {
	o := siteScanOps(t)
	o.HTTP = &http.Client{Transport: failHTTP{}}
	c := config.Default()
	c.Strategy = &config.Strategy{ID: "tt-multidisorder", TCPTLS: "--payload=tls_client_hello --lua-desync=multidisorder:pos=2:seqovl=1"}
	if err := config.Save(o.P, c); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(o.P.ConfigFile)
	rep, err := o.Scan(context.Background(), scan.Options{ASN: 9121, Site: "pastebin.com"}, nil)
	if !errors.Is(err, scan.ErrNoStrategy) || len(rep.Trials) != 1 || rep.Trials[0].OK {
		t.Fatalf("err %v, trials %+v", err, rep.Trials)
	}
	if after, _ := os.ReadFile(o.P.ConfigFile); string(after) != string(before) {
		t.Errorf("config changed:\n%s", after)
	}
	if sites, _ := o.SitesList(); len(sites) != 0 {
		t.Errorf("sites %q", sites)
	}
}

// With no apps, a user site alone is enough to scan, and every trial probes it.
func TestScanSitesWithoutApps(t *testing.T) {
	o, _, _ := trialOps(t, fstest.MapFS{"proc/net/netfilter/nfnetlink_queue": {Data: []byte(queueBound)}})
	if _, err := o.Scan(context.Background(), scan.Options{ASN: 9121, NoBlockcheck: true}, nil); !errors.As(err, &NoAppsError{}) {
		t.Fatalf("no apps, no sites: got %v, want NoAppsError", err)
	}
	h := &fakeHTTP{}
	o.HTTP = &http.Client{Transport: h}
	if _, err := o.SitesAdd("pastebin.com"); err != nil {
		t.Fatal(err)
	}
	rep, err := o.Scan(context.Background(), scan.Options{ASN: 9121, NoBlockcheck: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Winner == nil || !slices.Equal(h.seen(), []string{"https://pastebin.com/"}) {
		t.Errorf("winner %+v, probed %q", rep.Winner, h.seen())
	}
}
