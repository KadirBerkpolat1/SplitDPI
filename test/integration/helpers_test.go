//go:build integration

package integration_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"

	"github.com/KadirBerkpolat1/SplitDPI/internal/doctor"
	"github.com/KadirBerkpolat1/SplitDPI/internal/paths"
	"github.com/KadirBerkpolat1/SplitDPI/internal/supervisor"
)

// Everything runs inside two throwaway network namespaces. Nothing here
// touches the host's own nft tables or processes.
const (
	srvNS    = "sdpi-srv"
	cliNS    = "sdpi-cli"
	srvIP    = "10.99.0.1"
	cliIP    = "10.99.0.2"
	testHost = "test.local"
	strategy = "--payload=tls_client_hello --lua-desync=multisplit:pos=2"
	// sdpi-srv also holds the first DoH upstream's address and serves a fake
	// DoH there, so the forwarder test needs no internet.
	dohIP     = "1.1.1.1"
	dohAnswer = "10.99.0.1"
)

var (
	bin    string    // the splitdpi binary under test (SPLITDPI_BIN)
	shared string    // root shared by the run → kill -9 → revert tests
	runCmd *exec.Cmd // the `splitdpi run` started by TestRunPassesTrafficAndQueues
	pki    string    // test CA and DoH certificate; splitdpi trusts it via SSL_CERT_FILE
)

func TestMain(m *testing.M) {
	if os.Getenv("SDPI_TLS_SERVER") == "1" {
		serveTLS()
		return
	}
	if os.Geteuid() != 0 {
		fmt.Fprintln(os.Stderr, "integration tests need root: sudo -E SPLITDPI_BIN=... go test -tags integration ./test/integration/...")
		os.Exit(1)
	}
	if bin = os.Getenv("SPLITDPI_BIN"); bin == "" {
		fmt.Fprintln(os.Stderr, "SPLITDPI_BIN is not set")
		os.Exit(1)
	}
	os.Exit(runSuite(m))
}

func runSuite(m *testing.M) int {
	var srv *exec.Cmd
	cleanup := func() {
		stopRun()
		if srv != nil && srv.Process != nil {
			srv.Process.Kill()
			srv.Wait()
		}
		teardownNetns()
		if shared != "" {
			os.RemoveAll(shared)
		}
		if pki != "" {
			os.RemoveAll(pki)
		}
	}
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigs
		cleanup()
		os.Exit(1)
	}()
	defer cleanup()

	teardownNetns() // leftovers from an interrupted run
	if err := setupNetns(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	var err error
	if pki, err = makePKI(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if srv, err = startServer(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if shared, err = makeRoot(""); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return m.Run()
}

func sh(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, out)
	}
	return string(out), nil
}

func setupNetns() error {
	for _, c := range [][]string{
		{"ip", "netns", "add", srvNS},
		{"ip", "netns", "add", cliNS},
		{"ip", "link", "add", "sdpi-v0", "type", "veth", "peer", "name", "sdpi-v1"},
		{"ip", "link", "set", "sdpi-v0", "netns", srvNS},
		{"ip", "link", "set", "sdpi-v1", "netns", cliNS},
		{"ip", "-n", srvNS, "addr", "add", srvIP + "/24", "dev", "sdpi-v0"},
		{"ip", "-n", cliNS, "addr", "add", cliIP + "/24", "dev", "sdpi-v1"},
		{"ip", "-n", srvNS, "link", "set", "sdpi-v0", "up"},
		{"ip", "-n", cliNS, "link", "set", "sdpi-v1", "up"},
		{"ip", "-n", srvNS, "link", "set", "lo", "up"},
		{"ip", "-n", cliNS, "link", "set", "lo", "up"},
		{"ip", "-n", srvNS, "addr", "add", dohIP + "/32", "dev", "sdpi-v0"},
		// Public resolver IPs need a route before nft can redirect port 53.
		{"ip", "-n", cliNS, "route", "add", "default", "via", srvIP},
	} {
		if _, err := sh(c[0], c[1:]...); err != nil {
			return err
		}
	}
	return nil
}

// teardownNetns deletes both namespaces, which also removes the veth pair and
// every nft table created inside them.
func teardownNetns() {
	exec.Command("ip", "netns", "del", srvNS).Run()
	exec.Command("ip", "netns", "del", cliNS).Run()
}

// startServer re-executes this test binary as a TLS server inside sdpi-srv.
func startServer() (*exec.Cmd, error) {
	cmd := exec.Command("ip", "netns", "exec", srvNS, os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), "SDPI_TLS_SERVER=1", "SDPI_PKI="+pki)
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	for range 50 {
		if curl() == nil {
			return cmd, nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	cmd.Process.Kill()
	cmd.Wait()
	return nil, errors.New("TLS server in " + srvNS + " did not come up")
}

// serveTLS answers every request for test.local with an 8 KB body, large
// enough that nfqws2's autohostlist does not count the close as a failure.
func serveTLS() {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		panic(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: testHost},
		DNSNames:     []string{testHost},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		panic(err)
	}
	cert := tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
	ln, err := tls.Listen("tcp", srvIP+":443", &tls.Config{Certificates: []tls.Certificate{cert}})
	if err != nil {
		panic(err)
	}
	go serveDoH(os.Getenv("SDPI_PKI"))
	body := strings.Repeat("x", 8192)
	panic(http.Serve(ln, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, body)
	})))
}

// makePKI writes a throwaway CA (ca.pem) and a certificate for the DoH
// address signed by it (doh.pem, doh-key.pem).
func makePKI() (string, error) {
	dir, err := os.MkdirTemp("", "sdpi-pki-")
	if err != nil {
		return "", err
	}
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", err
	}
	ca := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "splitdpi test CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, ca, ca, &caKey.PublicKey, caKey)
	if err != nil {
		return "", err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", err
	}
	leaf := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: dohIP},
		IPAddresses: []net.IP{net.ParseIP(dohIP)},
		NotBefore:   time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leaf, ca, &key.PublicKey, caKey)
	if err != nil {
		return "", err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return "", err
	}
	for name, block := range map[string]*pem.Block{
		"ca.pem":      {Type: "CERTIFICATE", Bytes: caDER},
		"doh.pem":     {Type: "CERTIFICATE", Bytes: leafDER},
		"doh-key.pem": {Type: "EC PRIVATE KEY", Bytes: keyDER},
	} {
		if err := os.WriteFile(filepath.Join(dir, name), pem.EncodeToMemory(block), 0o644); err != nil {
			return "", err
		}
	}
	return dir, nil
}

// serveDoH answers every A question with dohAnswer, on dohIP:443.
func serveDoH(pkiDir string) {
	cert, err := tls.LoadX509KeyPair(filepath.Join(pkiDir, "doh.pem"), filepath.Join(pkiDir, "doh-key.pem"))
	if err != nil {
		panic(err)
	}
	ln, err := tls.Listen("tcp", dohIP+":443", &tls.Config{Certificates: []tls.Certificate{cert}})
	if err != nil {
		panic(err)
	}
	answer := netip.MustParseAddr(dohAnswer).As4()
	panic(http.Serve(ln, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var q dnsmessage.Message
		if err := q.Unpack(body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		resp := dnsmessage.Message{Header: dnsmessage.Header{ID: q.ID, Response: true, RecursionAvailable: true}, Questions: q.Questions}
		for _, qq := range q.Questions {
			if qq.Type == dnsmessage.TypeA {
				resp.Answers = append(resp.Answers, dnsmessage.Resource{
					Header: dnsmessage.ResourceHeader{Name: qq.Name, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET, TTL: 60},
					Body:   &dnsmessage.AResource{A: answer},
				})
			}
		}
		b, _ := resp.Pack()
		w.Header().Set("Content-Type", "application/dns-message")
		w.Write(b)
	})))
}

// dig asks a public resolver IP for name from sdpi-cli and returns the A answers.
func dig(resolver, name string) (string, error) {
	out, err := sh("ip", "netns", "exec", cliNS, "dig", "+short", "+time=2", "+tries=1", "@"+resolver, name)
	return strings.TrimSpace(out), err
}

func curl() error {
	_, err := sh("ip", "netns", "exec", cliNS, "curl", "-sk", "--max-time", "5",
		"--resolve", testHost+":443:"+srvIP, "https://"+testHost+"/")
	return err
}

// makeRoot builds a SPLITDPI_ROOT with the real engine installed from the
// cached tarball and a config that desyncs test.local. extra holds more
// top-level config lines, e.g. "dns = true".
func makeRoot(extra string) (string, error) {
	dir, err := os.MkdirTemp("", "sdpi-it-")
	if err != nil {
		return "", err
	}
	// nfqws2 drops to an unprivileged user and must still reach its files.
	if err := os.Chmod(dir, 0o755); err != nil {
		return "", err
	}
	tarball := os.Getenv("SPLITDPI_ENGINE_TARBALL")
	if tarball == "" {
		tarball = "/tmp/z2/zapret2-v1.0.6.tar.gz"
	}
	if _, err := sh("env", "SPLITDPI_ROOT="+dir, bin, "engine", "install", "--from", tarball); err != nil {
		return "", err
	}
	p := paths.Under(dir)
	if err := os.MkdirAll(p.ConfigDir, 0o755); err != nil {
		return "", err
	}
	cfg := fmt.Sprintf("mode = \"apps\"\n%s\n[strategy]\nid = \"it-multisplit\"\ntcp_tls = %q\n", extra, strategy)
	if err := os.WriteFile(p.ConfigFile, []byte(cfg), 0o644); err != nil {
		return "", err
	}
	return dir, os.WriteFile(p.UserHosts, []byte(testHost+"\n"), 0o644)
}

// envFor is the environment for a splitdpi process using root. It trusts the
// test CA, so the DoH forwarder accepts the fake upstream.
func envFor(root string) []string {
	// LC_ALL=C: the tests check English output whatever the host's locale.
	return append(os.Environ(), "SPLITDPI_ROOT="+root, "SSL_CERT_FILE="+filepath.Join(pki, "ca.pem"), "LC_ALL=C")
}

// splitdpi runs the binary inside sdpi-cli and returns its output and exit code.
func splitdpi(root string, args ...string) (string, int) {
	cmd := exec.Command("ip", append([]string{"netns", "exec", cliNS, bin}, args...)...)
	cmd.Env = envFor(root)
	out, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return string(out), exit.ExitCode()
	}
	if err != nil {
		return string(out) + err.Error(), -1
	}
	return string(out), 0
}

// startRun starts `splitdpi run` in sdpi-cli and in a private PID namespace,
// so doctor sees only its own processes and not the host's bypass tools.
// A shell stays PID 1 there, so killing splitdpi does not tear the namespace
// down: nfqws2 can only die through Pdeathsig.
func startRun(root string, args ...string) (*exec.Cmd, error) {
	script := `"$@" & pid=$!; wait $pid; s=$?; sleep 5; exit $s`
	cmd := exec.Command("ip", append([]string{"netns", "exec", cliNS,
		"unshare", "--pid", "--fork", "--mount-proc", "--kill-child", "sh", "-c", script, "sh", bin}, args...)...)
	cmd.Env = envFor(root)
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	return cmd, cmd.Start()
}

func stopRun() {
	if runCmd != nil && runCmd.Process != nil {
		runCmd.Process.Kill() // --kill-child takes the whole PID namespace with it
		runCmd.Wait()
		runCmd = nil
	}
}

// descendants lists the live processes below pid (host PID namespace view).
func descendants(pid int) []doctor.Proc {
	files, _ := filepath.Glob("/proc/[0-9]*/stat")
	children := map[int][]doctor.Proc{}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		if p, ok := doctor.ParseProcStat(string(b)); ok {
			children[p.PPID] = append(children[p.PPID], p)
		}
	}
	var out []doctor.Proc
	queue := []int{pid}
	for len(queue) > 0 {
		for _, c := range children[queue[0]] {
			out = append(out, c)
			queue = append(queue, c.PID)
		}
		queue = queue[1:]
	}
	return out
}

func findComm(ps []doctor.Proc, comm string) (doctor.Proc, bool) {
	for _, p := range ps {
		if p.Comm == comm {
			return p, true
		}
	}
	return doctor.Proc{}, false
}

func waitFor(t *testing.T, d time.Duration, what string, ok func() bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	for !ok() {
		select {
		case <-ctx.Done():
			t.Fatalf("timed out after %s waiting for %s", d, what)
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func state(root string) string {
	st, _ := supervisor.ReadStatus(paths.Under(root))
	return st.State
}

func tableExists() bool {
	_, err := sh("ip", "netns", "exec", cliNS, "nft", "list", "table", "inet", "splitdpi")
	return err == nil
}

var counterRe = regexp.MustCompile(`counter packets (\d+) bytes \d+ queue`)

// queuedPackets sums the counters of every queue rule in our table.
func queuedPackets(t *testing.T) int {
	t.Helper()
	out, err := sh("ip", "netns", "exec", cliNS, "nft", "list", "table", "inet", "splitdpi")
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, m := range counterRe.FindAllStringSubmatch(out, -1) {
		v, _ := strconv.Atoi(m[1])
		n += v
	}
	return n
}
