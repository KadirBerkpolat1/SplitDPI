package install_test

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
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

func TestInstallSuccess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix install.sh")
	}
	bin := []byte("#!/bin/sh\necho splitdpi-test\n")
	sum := sha256.Sum256(bin)
	sums := fmt.Sprintf("%s  splitdpi-linux-amd64\n", hex.EncodeToString(sum[:]))
	ver := "v0.1.0-test"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/download/" + ver + "/splitdpi-linux-amd64":
			_, _ = w.Write(bin)
		case "/download/" + ver + "/sha256sums.txt":
			_, _ = w.Write([]byte(sums))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	prefix := t.TempDir()
	out, err := runInstall(t, []string{
		"SPLITDPI_VERSION=" + ver,
		"SPLITDPI_BASE_URL=" + srv.URL,
		"SPLITDPI_PREFIX=" + prefix,
	})
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
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)

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
			out, err := runInstall(t, []string{
				"SPLITDPI_VERSION=v0.1.0-ro",
				"SPLITDPI_BASE_URL=" + srv.URL,
				"SPLITDPI_PREFIX=" + prefix,
			})
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
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/download/" + ver + "/splitdpi-linux-amd64":
			_, _ = w.Write(bin)
		case "/download/" + ver + "/sha256sums.txt":
			_, _ = w.Write([]byte(sums))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	prefix := t.TempDir()
	out, err := runInstall(t, []string{
		"SPLITDPI_VERSION=" + ver,
		"SPLITDPI_BASE_URL=" + srv.URL,
		"SPLITDPI_PREFIX=" + prefix,
	})
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
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/download/" + ver + "/splitdpi-linux-amd64":
			_, _ = w.Write(bin)
		case "/download/" + ver + "/sha256sums.txt":
			_, _ = w.Write([]byte(sums))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	prefix := t.TempDir()
	_, err := runInstall(t, []string{
		"SPLITDPI_VERSION=" + ver,
		"SPLITDPI_BASE_URL=" + srv.URL,
		"SPLITDPI_PREFIX=" + prefix,
	})
	if err == nil {
		t.Fatal("want install failure")
	}
	if _, err := os.Stat(filepath.Join(prefix, "bin", "splitdpi")); !os.IsNotExist(err) {
		t.Fatal("binary installed after bad checksum")
	}
}
