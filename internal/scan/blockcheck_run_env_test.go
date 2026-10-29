package scan

import (
	"slices"
	"strings"
	"testing"

	"github.com/KadirBerkpolat1/SplitDPI/internal/engine"
)

func TestBlockcheckCmdEnvSimulate(t *testing.T) {
	t.Setenv("SPLITDPI_ROOT", t.TempDir())
	t.Setenv("SPLITDPI_BLOCKCHECK_SIMULATE", "1")
	env := blockcheckCmdEnv(engine.Engine{Dir: "/opt/zapret2", ArchDir: "linux-x86_64"}, BlockcheckParams{})
	if !slices.Contains(env, "SIMULATE=1") {
		t.Fatalf("missing SIMULATE=1 in %v", env)
	}
}

func TestBlockcheckCmdEnvSimulateRequiresRoot(t *testing.T) {
	t.Setenv("SPLITDPI_ROOT", "")
	t.Setenv("SPLITDPI_BLOCKCHECK_SIMULATE", "1")
	env := blockcheckCmdEnv(engine.Engine{Dir: "/opt/zapret2", ArchDir: "linux-x86_64"}, BlockcheckParams{})
	for _, e := range env {
		if strings.HasPrefix(e, "SIMULATE=") {
			t.Fatalf("SIMULATE set without SPLITDPI_ROOT: %v", env)
		}
	}
}
