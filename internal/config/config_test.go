package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/KadirBerkpolat1/SplitDPI/internal/config"
	"github.com/KadirBerkpolat1/SplitDPI/internal/paths"
)

func TestConfigRoundTrip(t *testing.T) {
	root := t.TempDir()
	p := paths.Under(root)
	def := config.Default()
	def.Apps = []string{"discord"}
	if err := config.Save(p, def); err != nil {
		t.Fatal(err)
	}
	got, err := config.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if got.Mode != def.Mode || got.QNum != def.QNum || len(got.Apps) != 1 || got.Apps[0] != "discord" {
		t.Fatalf("Load mismatch: %+v", got)
	}
}

func TestConfigBadTOML(t *testing.T) {
	root := t.TempDir()
	p := paths.Under(root)
	if err := os.MkdirAll(p.ConfigDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.ConfigFile, []byte("mode = broad\napps = [\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := config.Load(p)
	if err == nil {
		t.Fatal("want error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "config.toml") || !strings.Contains(msg, "line") {
		t.Fatalf("error %q should name config.toml and line", msg)
	}
}

func loadText(t *testing.T, text string) (config.Config, error) {
	t.Helper()
	p := paths.Under(t.TempDir())
	if err := os.MkdirAll(p.ConfigDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.ConfigFile, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	return config.Load(p)
}

// v0.1's `dns = true` was auto over DoH; anything else means off.
func TestDNSMigration(t *testing.T) {
	for text, want := range map[string]string{
		"dns = true\n":                         "auto/doh",
		"dns = false\n":                        "off/doh",
		"mode = \"broad\"\n":                   "off/doh",
		"dns = true\ndns_provider = \"off\"\n": "off/doh",
		"dns_provider = \"adguard\"\ndns_protocol = \"dot\"\n": "adguard/dot",
	} {
		c, err := loadText(t, text)
		if err != nil {
			t.Fatalf("%q: %v", text, err)
		}
		if got := c.DNSProvider + "/" + string(c.DNSProtocol); got != want {
			t.Errorf("%q: %s, want %s", text, got, want)
		}
		if c.DNSOn() != (c.DNSProvider != "off") {
			t.Errorf("%q: DNSOn %v", text, c.DNSOn())
		}
	}
	if c, _ := loadText(t, ""); c.DNSOn() {
		t.Error("missing config has DNS on")
	}
}

func TestDNSInvalid(t *testing.T) {
	for _, text := range []string{"dns_provider = \"on\"\n", "dns_protocol = \"doq\"\n"} {
		if _, err := loadText(t, text); err == nil || !strings.Contains(err.Error(), "config.toml") {
			t.Errorf("%q: %v", text, err)
		}
	}
}

// Saving a migrated config writes the new keys and drops `dns`.
func TestDNSSaveDropsOldKey(t *testing.T) {
	p := paths.Under(t.TempDir())
	if err := os.MkdirAll(p.ConfigDir, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(p.ConfigFile, []byte("dns = true\n"), 0o644)
	c, err := config.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := config.Save(p, c); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p.ConfigFile)
	if s := string(b); strings.Contains(s, "dns =") || !strings.Contains(s, `dns_provider = "auto"`) || !strings.Contains(s, `dns_protocol = "doh"`) {
		t.Errorf("saved:\n%s", s)
	}
}

func TestListAddRemove(t *testing.T) {
	path := filepath.Join(t.TempDir(), "exclude.txt")
	if err := os.WriteFile(path, []byte("# keep\n\nfoo.com\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := config.AddToList(path, "bar.com"); err != nil {
		t.Fatal(err)
	}
	if err := config.AddToList(path, "bar.com"); err != nil {
		t.Fatal(err)
	}
	lines, err := config.ReadList(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 2 || lines[0] != "foo.com" || lines[1] != "bar.com" {
		t.Fatalf("ReadList = %v", lines)
	}
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), "# keep") {
		t.Fatal("comments not preserved")
	}
	if err := config.RemoveFromList(path, "missing.com"); err != nil {
		t.Fatal(err)
	}
	lines, _ = config.ReadList(path)
	if len(lines) != 2 {
		t.Fatalf("Remove absent should be no-op, got %v", lines)
	}
	if err := config.RemoveFromList(path, "foo.com"); err != nil {
		t.Fatal(err)
	}
	lines, _ = config.ReadList(path)
	if len(lines) != 1 || lines[0] != "bar.com" {
		t.Fatalf("after remove foo: %v", lines)
	}
}

func TestListAddAfterLineWithoutNewline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hosts.txt")
	if err := os.WriteFile(path, []byte("foo.com"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := config.AddToList(path, "bar.com"); err != nil {
		t.Fatal(err)
	}
	lines, err := config.ReadList(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 2 || lines[0] != "foo.com" || lines[1] != "bar.com" {
		t.Fatalf("ReadList = %q", lines)
	}
}

func TestListAddMany(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hosts.txt")
	if err := os.WriteFile(path, []byte("foo.com"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := config.AddToList(path, "bar.com", "foo.com", "baz.com", "bar.com"); err != nil {
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(path); string(raw) != "foo.com\nbar.com\nbaz.com\n" {
		t.Fatalf("file %q", raw)
	}
}

// The autohostlist is owned by the nfqws2 user; replacing it must not take that away.
func TestListKeepsModeAndOwner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "autohostlist.txt")
	if err := os.WriteFile(path, []byte("a.com\nb.com\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	const uid, gid = 4242, 4243
	root := os.Geteuid() == 0
	if root {
		if err := os.Chown(path, uid, gid); err != nil {
			t.Fatal(err)
		}
	}
	check := func(step string) {
		t.Helper()
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != 0o644 {
			t.Errorf("%s: mode %o, want 644", step, fi.Mode().Perm())
		}
		if st := fi.Sys().(*syscall.Stat_t); root && (st.Uid != uid || st.Gid != gid) {
			t.Errorf("%s: owner %d:%d, want %d:%d", step, st.Uid, st.Gid, uid, gid)
		}
	}
	if err := config.RemoveFromList(path, "a.com"); err != nil {
		t.Fatal(err)
	}
	check("remove")
	if err := config.AddToList(path, "c.com"); err != nil {
		t.Fatal(err)
	}
	check("add")
	if !root {
		t.Log("not root: owner not checked")
	}
}
