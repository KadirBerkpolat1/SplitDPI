package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEngineUsage(t *testing.T) {
	for _, args := range [][]string{{"engine"}, {"engine", "frob"}, {"engine", "install", "--bogus"}, {"engine", "install", "--from"}} {
		if _, code := run(t, 0, args...); code != 2 {
			t.Errorf("%v: code %d, want 2", args, code)
		}
	}
}

func TestEngineNeedsRoot(t *testing.T) {
	out, code := run(t, 1000, "engine", "status")
	eq(t, code, 1)
	has(t, out, "sudo splitdpi engine status")
}

func TestEngineStatusNotInstalled(t *testing.T) {
	out, code := run(t, 0, "engine", "status")
	eq(t, code, 1)
	has(t, out, "sudo splitdpi engine install")
}

func TestEngineInstallFromWrongFile(t *testing.T) {
	f := filepath.Join(t.TempDir(), "z.tar.gz")
	if err := os.WriteFile(f, []byte("not zapret2"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, code := run(t, 0, "engine", "install", "--from", f)
	eq(t, code, 1)
	has(t, out, "SHA-256")
}
