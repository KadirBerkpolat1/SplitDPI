package cli

import (
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
)

func zapret2Root() fstest.MapFS {
	root := systemdRoot()
	root["usr/lib/systemd/system/zapret2.service"] = &fstest.MapFile{}
	root["opt/zapret2/ipset/zapret-hosts-user.txt"] = &fstest.MapFile{Data: []byte("pastebin.com\nnot a domain\ndiscord.com\n")}
	root["proc/1156/stat"] = &fstest.MapFile{Data: []byte("1156 (nfqws2) S 1 1 1 0 -1 0")}
	return root
}

func TestTakeoverNone(t *testing.T) {
	o, _ := runOpsFor(t, systemdRoot())
	out, code := runOps(t, o, "takeover")
	eq(t, code, 0)
	has(t, out, "No other zapret install")
}

func TestTakeoverPlanChangesNothing(t *testing.T) {
	o, f := runOpsFor(t, zapret2Root())
	f.out = map[string]string{"systemctl is-enabled zapret2": "enabled"}
	out, code := runOps(t, o, "takeover")
	eq(t, code, 0)
	for _, s := range []string{"Found zapret2", "starts on boot", "running now", "stop the zapret2 service", "keep zapret2 from starting on boot", "sudo splitdpi takeover --yes"} {
		has(t, out, s)
	}
	if f.saw("systemctl stop zapret2") || f.saw("systemctl disable zapret2") {
		t.Errorf("plan changed the service: %q", f.calls)
	}
	if _, err := os.Stat(o.P.UserHosts); !os.IsNotExist(err) {
		t.Errorf("plan imported sites: %v", err)
	}
}

func TestTakeoverYesAndRevert(t *testing.T) {
	o, f := runOpsFor(t, zapret2Root())
	f.out = map[string]string{"systemctl is-enabled zapret2": "enabled"}
	out, code := runOps(t, o, "takeover", "--yes")
	eq(t, code, 0)
	for _, s := range []string{"added 2 new sites", "Line 2 skipped, not a domain: not a domain", "stopped the zapret2 service", "no longer starts on boot", "Done"} {
		has(t, out, s)
	}
	if !f.saw("systemctl stop zapret2") || !f.saw("systemctl disable zapret2") {
		t.Errorf("calls %q", f.calls)
	}
	out, code = runOps(t, o, "revert")
	eq(t, code, 0)
	has(t, out, "Turned back on: zapret2 (systemd)")
	if !f.saw("systemctl enable zapret2") || !f.saw("systemctl start zapret2") {
		t.Errorf("calls %q", f.calls)
	}
}

func TestTakeoverUsage(t *testing.T) {
	_, code := run(t, 0, "takeover", "--force")
	eq(t, code, 2)
}

func TestSitesImport(t *testing.T) {
	o, _ := runOpsFor(t, fstest.MapFS{})
	file := filepath.Join(t.TempDir(), "sites.txt")
	os.WriteFile(file, []byte("# mine\n*.pastebin.com\nhttp://\n"), 0o644)
	out, code := runOps(t, o, "sites", "import", file)
	eq(t, code, 0)
	has(t, out, "Line 3 skipped, not a domain: http://")
	has(t, out, "Added 1 new sites")
	out, _ = runOps(t, o, "sites", "list")
	eq(t, out, "pastebin.com\n")
}
