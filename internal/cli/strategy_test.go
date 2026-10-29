package cli

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/KadirBerkpolat1/SplitDPI/internal/config"
)

func TestStrategySetAndList(t *testing.T) {
	o := rootOps(t)
	o.Now = time.Now
	out, code := runOps(t, o, "strategy", "set", "tc-multidisorder")
	eq(t, code, 0)
	has(t, out, "tc-multidisorder")
	has(t, out, "Turkcell")
	if strings.Contains(out, "restart it") {
		t.Errorf("restart hint while not running: %q", out)
	}
	if c, _ := config.Load(o.P); c.Strategy == nil || c.Strategy.ID != "tc-multidisorder" {
		t.Fatalf("saved %+v", c.Strategy)
	}
	out, code = runOps(t, o, "strategy", "list")
	eq(t, code, 0)
	has(t, out, "* tc-multidisorder")
	has(t, out, "  generic-multidisorder")
	has(t, out, "generic")
}

func TestStrategySetUnknown(t *testing.T) {
	o := rootOps(t)
	o.Now = time.Now
	runOps(t, o, "strategy", "set", "tt-multidisorder")
	before, err := os.ReadFile(o.P.ConfigFile)
	if err != nil {
		t.Fatal(err)
	}
	out, code := runOps(t, o, "strategy", "set", "nope")
	eq(t, code, 1)
	has(t, out, `"nope"`)
	if after, _ := os.ReadFile(o.P.ConfigFile); !bytes.Equal(before, after) {
		t.Errorf("config changed:\n%s", after)
	}
}
