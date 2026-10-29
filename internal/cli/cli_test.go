package cli

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/KadirBerkpolat1/SplitDPI/internal/ops"
	"github.com/KadirBerkpolat1/SplitDPI/internal/paths"
)

// The CLI picks its language from the environment; assertions are in English.
func TestMain(m *testing.M) {
	os.Setenv("LC_ALL", "C")
	os.Exit(m.Run())
}

func eq[T comparable](t *testing.T, got, want T) {
	t.Helper()
	if got != want {
		t.Errorf("got %v, want %v", got, want)
	}
}

func has(t *testing.T, s, sub string) {
	t.Helper()
	if !strings.Contains(s, sub) {
		t.Errorf("%q does not contain %q", s, sub)
	}
}

func run(t *testing.T, euid int, args ...string) (string, int) {
	t.Helper()
	o := &ops.Ops{P: paths.Under(t.TempDir()), Euid: func() int { return euid }, GOARCH: "amd64"}
	var out bytes.Buffer
	code := Main(args, &out, &out, o)
	return out.String(), code
}

func TestVersion(t *testing.T) {
	out, code := run(t, 1000, "version")
	eq(t, code, 0)
	has(t, out, "splitdpi dev")
}

func TestNeedsRoot(t *testing.T) {
	out, code := run(t, 1000, "doctor")
	eq(t, code, 1)
	has(t, out, "sudo splitdpi doctor")
}

// No arguments opens the TUI, which runs as root like every other command.
func TestNoArgsNeedsRoot(t *testing.T) {
	out, code := run(t, 1000)
	eq(t, code, 1)
	has(t, out, "sudo splitdpi")
}

func TestNoArgsWithoutTerminalPrintsUsage(t *testing.T) {
	out, code := run(t, 0)
	eq(t, code, 2)
	has(t, out, "Usage")
}

func TestUnknown(t *testing.T) {
	_, code := run(t, 0, "frobnicate")
	eq(t, code, 2)
}

func TestLangFlag(t *testing.T) {
	out, _ := run(t, 1000, "--lang", "en", "--help")
	has(t, out, "Usage")
}

// The language saved in the TUI's settings wins over the environment, and
// --lang over both.
func TestLangFromConfig(t *testing.T) {
	o := &ops.Ops{P: paths.Under(t.TempDir()), Euid: func() int { return 1000 }, GOARCH: "amd64"}
	if err := o.SetLang("tr"); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	Main([]string{"doctor"}, &out, &out, o)
	has(t, out.String(), "Bu komut root yetkisi ister")
	out.Reset()
	Main([]string{"--lang", "en", "doctor"}, &out, &out, o)
	has(t, out.String(), "needs root")
}
