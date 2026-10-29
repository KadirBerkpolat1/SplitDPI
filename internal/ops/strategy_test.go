package ops

import (
	"bytes"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/KadirBerkpolat1/SplitDPI/internal/config"
	"github.com/KadirBerkpolat1/SplitDPI/internal/engine"
	"github.com/KadirBerkpolat1/SplitDPI/internal/paths"
)

func strategyOps(t *testing.T) *Ops {
	return &Ops{P: paths.Under(t.TempDir()), Now: func() time.Time { return time.Date(2026, 10, 29, 9, 0, 0, 0, time.UTC) }}
}

func TestStrategyList(t *testing.T) {
	list, cur, err := strategyOps(t).StrategyList()
	if err != nil || cur != nil {
		t.Fatalf("cur %+v, err %v", cur, err)
	}
	from := map[string]string{}
	for _, ch := range list {
		if _, dup := from[ch.Strategy.ID]; dup {
			t.Errorf("id %s listed twice", ch.Strategy.ID)
		}
		from[ch.Strategy.ID] = ch.From()
	}
	if from["tt-multidisorder"] != "Türk Telekom" || from["generic-multidisorder"] != "generic" {
		t.Errorf("sources %v", from)
	}
}

func TestStrategySet(t *testing.T) {
	o := strategyOps(t)
	c := config.Default()
	c.Strategy = &config.Strategy{ID: "blockcheck", Source: "blockcheck2", TCPTLS: "--x", ASN: 9121}
	if err := config.Save(o.P, c); err != nil {
		t.Fatal(err)
	}
	ch, running, err := o.StrategySet("tc-multidisorder")
	if err != nil || running {
		t.Fatalf("running %v, err %v", running, err)
	}
	if ch.ISP != "Turkcell" {
		t.Errorf("choice %+v", ch)
	}
	got, _ := config.Load(o.P)
	want := config.Strategy{
		ID:     "tc-multidisorder",
		Source: "isp/tr/as16135-turkcell.toml",
		TCPTLS: ch.Strategy.TCPTLS,
		ASN:    9121,
		Date:   "2026-10-29",
		Engine: engine.Version,
	}
	if got.Strategy == nil || *got.Strategy != want || want.TCPTLS == "" {
		t.Errorf("saved %+v, want %+v", got.Strategy, want)
	}
}

// Without a scanned ASN, an ISP strategy stores that ISP's ASN so the
// country exclude list loads; generic strategies leave it unknown.
func TestStrategySetASNWithoutScan(t *testing.T) {
	for id, want := range map[string]int{"tc-multidisorder": 16135, "generic-multidisorder": 0} {
		o := strategyOps(t)
		if _, _, err := o.StrategySet(id); err != nil {
			t.Fatal(err)
		}
		if c, _ := config.Load(o.P); c.Strategy == nil || c.Strategy.ASN != want {
			t.Errorf("%s: saved %+v, want ASN %d", id, c.Strategy, want)
		}
	}
}

func TestStrategySetUnknownLeavesConfig(t *testing.T) {
	o := strategyOps(t)
	c := config.Default()
	c.Strategy = &config.Strategy{ID: "tt-multidisorder", TCPTLS: "--x"}
	if err := config.Save(o.P, c); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(o.P.ConfigFile)
	if err != nil {
		t.Fatal(err)
	}
	var unknown UnknownStrategyError
	if _, _, err := o.StrategySet("nope"); !errors.As(err, &unknown) || unknown.ID != "nope" {
		t.Fatalf("got %v, want UnknownStrategyError", err)
	}
	if after, _ := os.ReadFile(o.P.ConfigFile); !bytes.Equal(before, after) {
		t.Errorf("config changed:\n%s\nwas:\n%s", after, before)
	}
}
