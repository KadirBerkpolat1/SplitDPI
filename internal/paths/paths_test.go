package paths_test

import (
	"testing"

	"github.com/KadirBerkpolat1/SplitDPI/internal/paths"
)

func eq(t *testing.T, got, want string) {
	t.Helper()
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestUnder(t *testing.T) {
	p := paths.Under("/tmp/r")
	eq(t, p.ConfigFile, "/tmp/r/etc/splitdpi/config.toml")
	eq(t, p.AutoHostlist, "/tmp/r/var/lib/splitdpi/autohostlist.txt")
	eq(t, p.TakeoverFile, "/tmp/r/var/lib/splitdpi/takeover.json")
	eq(t, p.StatusFile, "/tmp/r/run/splitdpi/status.json")
	eq(t, p.SystemdUnit, "/tmp/r/etc/systemd/system/splitdpi.service")
	eq(t, p.RunitLink, "/tmp/r/var/service/splitdpi")
	eq(t, p.NftTable, "splitdpi")
	eq(t, p.HostlistFile, "/tmp/r/run/splitdpi/hostlist.txt")
	eq(t, p.ExcludeFile, "/tmp/r/run/splitdpi/exclude.txt")
	eq(t, paths.Under("").BinPath, "/usr/local/bin/splitdpi")
}
