package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/KadirBerkpolat1/SplitDPI/internal/config"
	"github.com/KadirBerkpolat1/SplitDPI/internal/ops"
	"github.com/KadirBerkpolat1/SplitDPI/internal/paths"
)

func runOps(t *testing.T, o *ops.Ops, args ...string) (string, int) {
	t.Helper()
	var out bytes.Buffer
	code := Main(args, &out, &out, o)
	return out.String(), code
}

func rootOps(t *testing.T) *ops.Ops {
	return &ops.Ops{P: paths.Under(t.TempDir()), Euid: func() int { return 0 }}
}

func TestPresetsListNonRoot(t *testing.T) {
	out, code := run(t, 1000, "presets", "list")
	eq(t, code, 0)
	has(t, out, "Türk Telekom")
	has(t, out, "embedded")
}

func TestAppsEnableUnknown(t *testing.T) {
	out, code := run(t, 0, "apps", "enable", "nosuch")
	eq(t, code, 1)
	has(t, out, "discord")
}

func TestAppsEnableDisable(t *testing.T) {
	o := rootOps(t)
	if _, code := runOps(t, o, "apps", "enable", "discord"); code != 0 {
		t.Fatalf("enable: code %d", code)
	}
	runOps(t, o, "apps", "enable", "discord")
	c, err := config.Load(o.P)
	if err != nil || !slices.Equal(c.Apps, []string{"discord"}) {
		t.Fatalf("apps after enable: %q, %v", c.Apps, err)
	}
	out, _ := runOps(t, o, "apps", "list")
	has(t, out, "discord")
	if _, code := runOps(t, o, "apps", "disable", "discord"); code != 0 {
		t.Fatalf("disable: code %d", code)
	}
	if c, _ := config.Load(o.P); len(c.Apps) != 0 {
		t.Errorf("apps after disable: %q", c.Apps)
	}
}

func TestAppsNeedRoot(t *testing.T) {
	out, code := run(t, 1000, "apps", "enable", "discord")
	eq(t, code, 1)
	has(t, out, "sudo splitdpi apps enable discord")
}

func TestExcludeAdd(t *testing.T) {
	o := rootOps(t)
	if err := os.MkdirAll(filepath.Dir(o.P.AutoHostlist), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(o.P.AutoHostlist, []byte("discord.com\nexample.com\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, code := runOps(t, o, "exclude", "add", "https://Example.com/x")
	eq(t, code, 0)
	has(t, out, "example.com")
	user, _ := config.ReadList(o.P.UserExclude)
	if !slices.Equal(user, []string{"example.com"}) {
		t.Errorf("user exclude: %q", user)
	}
	auto, _ := config.ReadList(o.P.AutoHostlist)
	if !slices.Equal(auto, []string{"discord.com"}) {
		t.Errorf("autohostlist: %q", auto)
	}
	out, _ = runOps(t, o, "exclude", "list")
	has(t, out, "example.com")
	if _, code := runOps(t, o, "exclude", "remove", "EXAMPLE.com"); code != 0 {
		t.Fatalf("remove: code %d", code)
	}
	if user, _ := config.ReadList(o.P.UserExclude); len(user) != 0 {
		t.Errorf("user exclude after remove: %q", user)
	}
}

func TestSites(t *testing.T) {
	o := rootOps(t)
	out, code := runOps(t, o, "sites", "add", "https://PasteBin.com/x")
	eq(t, code, 0)
	has(t, out, "pastebin.com")
	has(t, out, "Restart splitdpi")
	runOps(t, o, "sites", "add", "pastebin.com")
	out, _ = runOps(t, o, "sites", "list")
	eq(t, out, "pastebin.com\n")
	if _, code := runOps(t, o, "sites", "remove", "PASTEBIN.com"); code != 0 {
		t.Fatalf("remove: code %d", code)
	}
	if list, _ := config.ReadList(o.P.UserHosts); len(list) != 0 {
		t.Errorf("sites after remove: %q", list)
	}
	if _, code := runOps(t, o, "sites", "add", "1.2.3.4"); code != 1 {
		t.Errorf("junk: code %d, want 1", code)
	}
	out, code = run(t, 1000, "sites", "add", "pastebin.com")
	eq(t, code, 1)
	has(t, out, "sudo splitdpi sites add pastebin.com")
}

func TestExcludeAddRejectsJunk(t *testing.T) {
	_, code := runOps(t, rootOps(t), "exclude", "add", "1.2.3.4")
	eq(t, code, 1)
}

// `presets check DIR` is what the presets repo's CI runs, as a normal user.
func TestPresetsCheck(t *testing.T) {
	out, code := run(t, 1000, "presets", "check", "../presets/data")
	if code != 0 {
		t.Fatalf("seed data: exit %d:\n%s", code, out)
	}
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "apps"), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "apps", "good.toml"), []byte("schema = 1\nid = \"good\"\nname = \"G\"\nhosts = [\"good.example\"]\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "apps", "bad.toml"), []byte("schema = 1\nid = \"bad\"\nhosts = [\"bad host\"]\n"), 0o644)
	out, code = run(t, 1000, "presets", "check", dir)
	eq(t, code, 1)
	has(t, out, "apps/bad.toml")
	if _, code := run(t, 1000, "presets", "check"); code != 2 {
		t.Errorf("no DIR: code %d, want 2", code)
	}
}

func TestListsUsage(t *testing.T) {
	for _, args := range [][]string{{"presets"}, {"presets", "frob"}, {"presets", "update", "x"}, {"apps"}, {"apps", "enable"}, {"exclude", "add"}, {"exclude", "frob", "x"}, {"sites"}, {"sites", "add"}, {"sites", "--help"}, {"strategy"}, {"strategy", "set"}} {
		if _, code := run(t, 0, args...); code != 2 {
			t.Errorf("%v: code %d, want 2", args, code)
		}
	}
}
