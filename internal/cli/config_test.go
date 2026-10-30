package cli

import (
	"slices"
	"strings"
	"testing"

	"github.com/KadirBerkpolat1/SplitDPI/internal/config"
)

func TestConfigNeedsRoot(t *testing.T) {
	out, code := run(t, 1000, "config")
	eq(t, code, 1)
	has(t, out, "sudo splitdpi config")
}

func TestConfigList(t *testing.T) {
	o := rootOps(t)
	out, code := runOps(t, o, "config")
	eq(t, code, 0)
	want := []string{
		"lang         system language         (default)",
		"run_mode     window                  (default)",
		"mode         broad                   (default)",
		"skip_ifaces  wg*,tun*,tailscale*     (default)",
		"ipinfo_url   https://ipinfo.io/json  (default)",
		"qnum         200                     (default)",
	}
	lines := strings.Split(out, "\n")
	if len(lines) < len(want) || !slices.Equal(lines[:len(want)], want) {
		t.Fatalf("got\n%s\nwant\n%s", out, strings.Join(want, "\n"))
	}
	has(t, out, "Usage: splitdpi config")

	writeConfig(t, o.P, "lang = \"tr\"\nqnum = 300\nskip_ifaces = []\n")
	out, _ = runOps(t, o, "--lang", "en", "config")
	has(t, out, "lang         tr                      (default: system language)")
	has(t, out, "skip_ifaces  none                    (default: wg*,tun*,tailscale*)")
	has(t, out, "qnum         300                     (default: 200)")
}

func TestConfigGetSetReset(t *testing.T) {
	o := rootOps(t)
	for _, tc := range []struct{ key, value, shown, stored string }{
		{"lang", "ru", "ru", "ru"},
		{"run_mode", "background", "background", "background"},
		{"skip_ifaces", " wg0, tun* ,,wg0", "wg0,tun*", "wg0,tun*"},
		{"skip_ifaces", "", "none", ""},
		{"ipinfo_url", "https://ip.example/json", "https://ip.example/json", "https://ip.example/json"},
		{"qnum", "4242", "4242", "4242"},
	} {
		out, code := runOps(t, o, "--lang", "en", "config", "set", tc.key, tc.value)
		eq(t, code, 0)
		has(t, out, tc.key+" is now "+tc.shown+".")
		out, code = runOps(t, o, "config", "get", tc.key)
		eq(t, code, 0)
		eq(t, out, tc.stored+"\n")
	}

	for key, want := range map[string]string{
		"lang": "", "run_mode": "window", "skip_ifaces": "wg*,tun*,tailscale*",
		"ipinfo_url": "https://ipinfo.io/json", "qnum": "200", "mode": "broad",
	} {
		_, code := runOps(t, o, "--lang", "en", "config", "reset", key)
		eq(t, code, 0)
		out, _ := runOps(t, o, "config", "get", key)
		eq(t, out, want+"\n")
	}
	c, err := config.Load(o.P)
	if err != nil {
		t.Fatal(err)
	}
	if def := config.Default(); c.Lang != def.Lang || c.QNum != def.QNum || !slices.Equal(c.SkipIfaces, def.SkipIfaces) {
		t.Errorf("after reset %+v", c)
	}
}

func TestConfigRestartNotes(t *testing.T) {
	o := rootOps(t)
	out, _ := runOps(t, o, "config", "set", "qnum", "201")
	has(t, out, "NFQUEUE")
	has(t, out, "It applies the next time SplitDPI starts.")

	out, _ = runOps(t, o, "config", "set", "run_mode", "boot")
	if strings.Contains(out, "next time SplitDPI starts") {
		t.Errorf("run_mode needs no restart: %q", out)
	}

	holdLock(t, o.P, "service")
	out, _ = runOps(t, o, "config", "set", "skip_ifaces", "wg*")
	has(t, out, "splitdpi is running: restart it to apply")
}

func TestConfigRejects(t *testing.T) {
	o := rootOps(t)
	for _, tc := range []struct {
		args []string
		code int
		want []string
	}{
		{[]string{"set", "lang", "de"}, 1, []string{`"de"`, "en, tr, ru"}},
		{[]string{"set", "run_mode", "later"}, 1, []string{"window, background, boot"}},
		{[]string{"set", "mode", "all"}, 1, []string{"broad", "apps"}},
		{[]string{"set", "skip_ifaces", "wg0,bad name"}, 1, []string{`"bad name"`, "1-15"}},
		{[]string{"set", "ipinfo_url", "http://ipinfo.io/json"}, 1, []string{"https://", "config reset ipinfo_url"}},
		{[]string{"set", "qnum", "65536"}, 1, []string{"0 to 65535"}},
		{[]string{"set", "qnum", "two"}, 1, []string{"0 to 65535", "two"}},
		{[]string{"set", "dns", "auto"}, 2, []string{`"dns"`, "lang, run_mode, mode, skip_ifaces, ipinfo_url, qnum"}},
		{[]string{"get"}, 2, []string{"Usage: splitdpi config"}},
		{[]string{"set", "qnum"}, 2, []string{"Usage: splitdpi config"}},
		{[]string{"reset", "qnum", "1"}, 2, []string{"Usage: splitdpi config"}},
	} {
		out, code := runOps(t, o, append([]string{"--lang", "en", "config"}, tc.args...)...)
		if code != tc.code {
			t.Errorf("%q: code %d, want %d", tc.args, code, tc.code)
		}
		for _, w := range tc.want {
			has(t, out, w)
		}
	}
	if c, _ := config.Load(o.P); c.QNum != 200 || c.Mode != config.ModeBroad {
		t.Errorf("a refused value was saved: %+v", c)
	}
}

func TestConfigAppsModeNeedsSomething(t *testing.T) {
	o := rootOps(t)
	out, code := runOps(t, o, "--lang", "en", "config", "set", "mode", "apps")
	eq(t, code, 1)
	has(t, out, "sudo splitdpi apps enable ID")
	has(t, out, "sudo splitdpi sites add DOMAIN")
	if c, _ := config.Load(o.P); c.Mode != config.ModeBroad {
		t.Errorf("mode %s", c.Mode)
	}

	if _, code := runOps(t, o, "sites", "add", "example.com"); code != 0 {
		t.Fatal("sites add failed")
	}
	out, code = runOps(t, o, "--lang", "en", "config", "set", "mode", "apps")
	eq(t, code, 0)
	has(t, out, "mode is now apps.")
}

func TestConfigTurkish(t *testing.T) {
	o := rootOps(t)
	out, code := runOps(t, o, "--lang", "tr", "config")
	eq(t, code, 0)
	has(t, out, "lang         sistem dili             (varsayılan)")
	out, _ = runOps(t, o, "--lang", "tr", "config", "set", "mode", "apps")
	has(t, out, "en az bir açık uygulama")
}
