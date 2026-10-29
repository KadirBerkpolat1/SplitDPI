package cli

import (
	"os"
	"syscall"
	"testing"
	"testing/fstest"

	"github.com/KadirBerkpolat1/SplitDPI/internal/config"
)

func TestDNSOnOff(t *testing.T) {
	o, _ := runOpsFor(t, fstest.MapFS{})
	out, code := runOps(t, o, "dns", "on")
	eq(t, code, 0)
	has(t, out, "next start")
	if c, err := config.Load(o.P); err != nil || !c.DNS {
		t.Fatalf("dns on not saved: %+v %v", c, err)
	}
	if _, code := runOps(t, o, "dns", "off"); code != 0 {
		t.Fatalf("dns off: code %d", code)
	}
	if c, _ := config.Load(o.P); c.DNS {
		t.Error("dns off not saved")
	}
}

func TestDNSOnWhileRunningSaysRestart(t *testing.T) {
	o, _ := runOpsFor(t, fstest.MapFS{})
	if err := os.MkdirAll(o.P.RunDir, 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(o.P.LockFile, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	f.WriteString(`{"pid":1,"mode":"foreground"}`)
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	out, code := runOps(t, o, "dns", "on")
	eq(t, code, 0)
	has(t, out, "sudo splitdpi stop")
}

func TestStatusWarnsDNS(t *testing.T) {
	o, _ := runOpsFor(t, fstest.MapFS{})
	writeConfig(t, o.P, "dns = true\n")
	out, code := runOps(t, o, "status")
	eq(t, code, 0)
	has(t, out, "VPN split-DNS and LAN hostnames may break")
}

func TestDNSUsageAndRoot(t *testing.T) {
	for _, args := range [][]string{{"dns"}, {"dns", "maybe"}, {"dns", "on", "x"}} {
		if _, code := run(t, 0, args...); code != 2 {
			t.Errorf("%v: code %d, want 2", args, code)
		}
	}
	out, code := run(t, 1000, "dns", "on")
	eq(t, code, 1)
	has(t, out, "sudo splitdpi dns on")
}
