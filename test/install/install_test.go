package install_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("caller")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "../.."))
}

func runInstall(t *testing.T, env []string) (string, error) {
	t.Helper()
	cmd := exec.Command("sh", "install.sh")
	cmd.Dir = repoRoot(t)
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// tlsServer serves h over https; install.sh only downloads over https, so the
// returned env makes curl (and OpenSSL wget) trust the test certificate.
func tlsServer(t *testing.T, h http.Handler) (*httptest.Server, []string) {
	t.Helper()
	srv := httptest.NewTLSServer(h)
	t.Cleanup(srv.Close)
	ca := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}), 0o600); err != nil {
		t.Fatal(err)
	}
	return srv, []string{"CURL_CA_BUNDLE=" + ca, "SSL_CERT_FILE=" + ca}
}

func TestInstallSuccess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix install.sh")
	}
	bin := []byte("#!/bin/sh\necho splitdpi-test\n")
	sum := sha256.Sum256(bin)
	sums := fmt.Sprintf("%s  splitdpi-linux-amd64\n", hex.EncodeToString(sum[:]))
	ver := "v0.1.0-test"
	srv, trust := tlsServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/download/" + ver + "/splitdpi-linux-amd64":
			_, _ = w.Write(bin)
		case "/download/" + ver + "/sha256sums.txt":
			_, _ = w.Write([]byte(sums))
		default:
			http.NotFound(w, r)
		}
	}))

	prefix := t.TempDir()
	out, err := runInstall(t, append(trust,
		"SPLITDPI_VERSION="+ver,
		"SPLITDPI_BASE_URL="+srv.URL,
		"SPLITDPI_PREFIX="+prefix,
	))
	if err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	if !strings.Contains(out, "sudo splitdpi") {
		t.Fatalf("missing finish hint: %q", out)
	}
	installed := filepath.Join(prefix, "bin", "splitdpi")
	fi, err := os.Stat(installed)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&0o111 == 0 {
		t.Fatalf("not executable: %o", fi.Mode())
	}
}

// enable runs the binary as root only if no group or other may write to it
// or a directory above it. A caller's umask that lets the group write, as
// sudo may keep, must not leave the new prefix like that.
func TestInstallPrefixTrustedByEnable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix install.sh")
	}
	bin := []byte("#!/bin/sh\necho splitdpi-test\n")
	sum := sha256.Sum256(bin)
	sums := fmt.Sprintf("%s  splitdpi-linux-amd64\n", hex.EncodeToString(sum[:]))
	ver := "v0.1.0-umask"
	srv, tlsEnv := tlsServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/download/" + ver + "/splitdpi-linux-amd64":
			_, _ = w.Write(bin)
		case "/download/" + ver + "/sha256sums.txt":
			_, _ = w.Write([]byte(sums))
		default:
			http.NotFound(w, r)
		}
	}))

	parent := t.TempDir()
	prefix := filepath.Join(parent, "opt", "splitdpi")
	cmd := exec.Command("sh", "-c", "umask 002 && exec sh install.sh")
	cmd.Dir = repoRoot(t)
	cmd.Env = append(append(os.Environ(), tlsEnv...), "SPLITDPI_VERSION="+ver, "SPLITDPI_BASE_URL="+srv.URL, "SPLITDPI_PREFIX="+prefix)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	for _, p := range []string{filepath.Join(parent, "opt"), prefix, filepath.Join(prefix, "bin"), filepath.Join(prefix, "bin", "splitdpi")} {
		fi, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if perm := fi.Mode().Perm(); perm&0o022 != 0 {
			t.Errorf("%s: mode %o, a group or others may write", p, perm)
		}
	}
}

// /usr/local/bin usually exists, so a non-root run must be stopped by the
// writability check, not by a raw permission error from install.
func TestInstallNotWritableAsksForSudo(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix install.sh")
	}
	if os.Geteuid() == 0 {
		t.Skip("root can write anywhere")
	}
	hits := 0
	srv, trust := tlsServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		http.NotFound(w, r)
	}))

	for _, tc := range []struct {
		name   string
		mkBin  bool
		locked func(prefix string) string
	}{
		{"bin dir exists", true, func(p string) string { return filepath.Join(p, "bin") }},
		{"bin dir missing", false, func(p string) string { return p }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prefix := t.TempDir()
			if tc.mkBin {
				if err := os.Mkdir(filepath.Join(prefix, "bin"), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			locked := tc.locked(prefix)
			if err := os.Chmod(locked, 0o555); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { os.Chmod(locked, 0o755) })
			out, err := runInstall(t, append(trust,
				"SPLITDPI_VERSION=v0.1.0-ro",
				"SPLITDPI_BASE_URL="+srv.URL,
				"SPLITDPI_PREFIX="+prefix,
			))
			if err == nil {
				t.Fatal("want install failure")
			}
			if strings.TrimSpace(out) != "rerun with sudo" {
				t.Fatalf("unexpected output: %q", out)
			}
		})
	}
	if hits != 0 {
		t.Errorf("downloaded %d times before the permission check", hits)
	}
}

func TestInstallMissingChecksumLine(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix install.sh")
	}
	bin := []byte("fake-binary")
	sums := "0000000000000000000000000000000000000000000000000000000000000000  splitdpi-linux-arm64\n"
	ver := "v0.1.0-noline"
	srv, trust := tlsServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/download/" + ver + "/splitdpi-linux-amd64":
			_, _ = w.Write(bin)
		case "/download/" + ver + "/sha256sums.txt":
			_, _ = w.Write([]byte(sums))
		default:
			http.NotFound(w, r)
		}
	}))

	prefix := t.TempDir()
	out, err := runInstall(t, append(trust,
		"SPLITDPI_VERSION="+ver,
		"SPLITDPI_BASE_URL="+srv.URL,
		"SPLITDPI_PREFIX="+prefix,
	))
	if err == nil {
		t.Fatal("want install failure")
	}
	if !strings.Contains(out, "exactly one checksum line") {
		t.Fatalf("unexpected output: %q", out)
	}
	if _, err := os.Stat(filepath.Join(prefix, "bin", "splitdpi")); !os.IsNotExist(err) {
		t.Fatal("binary installed without asset checksum line")
	}
}

func TestInstallBadChecksum(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix install.sh")
	}
	bin := []byte("fake-binary")
	sums := "0000000000000000000000000000000000000000000000000000000000000000  splitdpi-linux-amd64\n"
	ver := "v0.1.0-bad"
	srv, trust := tlsServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/download/" + ver + "/splitdpi-linux-amd64":
			_, _ = w.Write(bin)
		case "/download/" + ver + "/sha256sums.txt":
			_, _ = w.Write([]byte(sums))
		default:
			http.NotFound(w, r)
		}
	}))

	prefix := t.TempDir()
	_, err := runInstall(t, append(trust,
		"SPLITDPI_VERSION="+ver,
		"SPLITDPI_BASE_URL="+srv.URL,
		"SPLITDPI_PREFIX="+prefix,
	))
	if err == nil {
		t.Fatal("want install failure")
	}
	if _, err := os.Stat(filepath.Join(prefix, "bin", "splitdpi")); !os.IsNotExist(err) {
		t.Fatal("binary installed after bad checksum")
	}
}

// A release fetched over plain http, directly or after a redirect from https,
// could be swapped together with its checksum by anyone on the path.
func TestInstallRefusesPlainHTTP(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix install.sh")
	}
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("install.sh enforces https through curl")
	}
	bin := []byte("#!/bin/sh\necho splitdpi-test\n")
	sum := sha256.Sum256(bin)
	sums := fmt.Sprintf("%s  splitdpi-linux-amd64\n", hex.EncodeToString(sum[:]))
	ver := "v0.1.0-http"
	var hits atomic.Int32
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		switch r.URL.Path {
		case "/download/" + ver + "/splitdpi-linux-amd64":
			_, _ = w.Write(bin)
		case "/download/" + ver + "/sha256sums.txt":
			_, _ = w.Write([]byte(sums))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(plain.Close)
	redirect, trust := tlsServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, plain.URL+r.URL.Path, http.StatusFound)
	}))

	for name, base := range map[string]string{"direct": plain.URL, "redirect": redirect.URL} {
		t.Run(name, func(t *testing.T) {
			prefix := t.TempDir()
			out, err := runInstall(t, append(trust,
				"SPLITDPI_VERSION="+ver,
				"SPLITDPI_BASE_URL="+base,
				"SPLITDPI_PREFIX="+prefix,
			))
			if err == nil {
				t.Fatalf("want install failure, got %q", out)
			}
			if _, err := os.Stat(filepath.Join(prefix, "bin", "splitdpi")); !os.IsNotExist(err) {
				t.Fatal("binary installed over plain http")
			}
		})
	}
	if n := hits.Load(); n != 0 {
		t.Errorf("plain http server got %d requests", n)
	}
}

func TestInstallRefusesHTTPBaseURL(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix install.sh")
	}
	prefix := t.TempDir()
	out, err := runInstall(t, []string{
		"SPLITDPI_BASE_URL=http://127.0.0.1:9/releases",
		"SPLITDPI_PREFIX=" + prefix,
	})
	if err == nil || !strings.Contains(out, "SPLITDPI_BASE_URL must start with https://") {
		t.Fatalf("err %v, output %q", err, out)
	}
}

// wget, GNU or BusyBox, follows a redirect from https to plain http, so
// without curl nothing is downloaded at all.
func TestInstallWithoutCurlRefuses(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix install.sh")
	}
	bin := t.TempDir()
	for _, tool := range []string{"uname", "sha256sum", "shasum", "id", "dirname", "mkdir", "mktemp", "rm", "grep", "install"} {
		if p, err := exec.LookPath(tool); err == nil {
			if err := os.Symlink(p, filepath.Join(bin, tool)); err != nil {
				t.Fatal(err)
			}
		}
	}
	ran := filepath.Join(t.TempDir(), "wget-ran")
	wget := "#!/bin/sh\n: >" + ran + "\nexit 1\n"
	if err := os.WriteFile(filepath.Join(bin, "wget"), []byte(wget), 0o755); err != nil {
		t.Fatal(err)
	}

	prefix := t.TempDir()
	out, err := runInstall(t, []string{
		"PATH=" + bin,
		"SPLITDPI_VERSION=v0.1.0-nocurl",
		"SPLITDPI_PREFIX=" + prefix,
	})
	if err == nil || !strings.Contains(out, "curl is required") {
		t.Fatalf("err %v, output %q", err, out)
	}
	if _, err := os.Stat(ran); err == nil {
		t.Error("install.sh ran wget")
	}
}
