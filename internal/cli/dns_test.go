package cli

import (
	"os"
	"syscall"
	"testing"
	"testing/fstest"

	"github.com/KadirBerkpolat1/SplitDPI/internal/config"
	"github.com/KadirBerkpolat1/SplitDPI/internal/dns"
)

func TestDNSOnOff(t *testing.T) {
	o, _ := runOpsFor(t, fstest.MapFS{})
	out, code := runOps(t, o, "dns", "on")
	eq(t, code, 0)
	has(t, out, "next start")
	if c, err := config.Load(o.P); err != nil || c.DNSProvider != "auto" || c.DNSProtocol != dns.DoH {
		t.Fatalf("dns on not saved as auto/doh: %+v %v", c, err)
	}
	if _, code := runOps(t, o, "dns", "off"); code != 0 {
		t.Fatalf("dns off: code %d", code)
	}
	if c, _ := config.Load(o.P); c.DNSOn() {
		t.Error("dns off not saved")
	}
}

func TestDNSProviderDoT(t *testing.T) {
	o, _ := runOpsFor(t, fstest.MapFS{})
	out, code := runOps(t, o, "dns", "--dot", "adguard")
	eq(t, code, 0)
	has(t, out, "AdGuard over DNS-over-TLS")
	has(t, out, "blocks ads and trackers")
	has(t, out, "VPN split-DNS")
	if c, _ := config.Load(o.P); c.DNSProvider != "adguard" || c.DNSProtocol != dns.DoT {
		t.Errorf("saved %s/%s", c.DNSProvider, c.DNSProtocol)
	}
}

func TestDNSShow(t *testing.T) {
	o, _ := runOpsFor(t, fstest.MapFS{})
	out, code := runOps(t, o, "dns")
	eq(t, code, 0)
	has(t, out, "Encrypted DNS is off")
	has(t, out, "Usage: splitdpi dns")
	writeConfig(t, o.P, "dns_provider = \"quad9\"\n")
	out, _ = runOps(t, o, "dns")
	has(t, out, "Encrypted DNS: Quad9 over DNS-over-HTTPS.")
	has(t, out, "malware")
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
	out, code := runOps(t, o, "dns", "cloudflare")
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
	for _, args := range [][]string{
		{"dns", "maybe"}, {"dns", "on", "x"}, {"dns", "google", "quad9"},
		{"dns", "--dot"}, {"dns", "off", "--dot"}, {"dns", "doq"},
	} {
		if _, code := run(t, 0, args...); code != 2 {
			t.Errorf("%v: code %d, want 2", args, code)
		}
	}
	out, code := run(t, 1000, "dns", "on")
	eq(t, code, 1)
	has(t, out, "sudo splitdpi dns on")
}
