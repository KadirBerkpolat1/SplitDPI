package ops

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/KadirBerkpolat1/SplitDPI/internal/config"
	"github.com/KadirBerkpolat1/SplitDPI/internal/paths"
)

// An app file with a [udp] table still loads, and an edit keeps the table.
const discordWithVoice = `schema = 1
id = "discord"
name = "Discord"
hosts = ["discord.com", "discord.media"]

[udp]
ports = "50000-50100"
l7 = ["discord", "stun"]
desync = "--payload=stun,discord_ip_discovery --lua-desync=fake:blob=fake_default_quic:repeats=6"
`

func TestAppEditShipped(t *testing.T) {
	o := &Ops{P: paths.Under(t.TempDir())}
	apps := filepath.Join(o.P.PresetCache, "apps")
	if err := os.MkdirAll(apps, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(apps, "discord.toml"), []byte(discordWithVoice), 0o644); err != nil {
		t.Fatal(err)
	}
	shipped, err := o.App("discord")
	if err != nil || shipped.UDP == nil || !slices.Contains(shipped.Hosts, "discord.media") {
		t.Fatalf("cached discord: %+v, %v", shipped, err)
	}
	if err := o.AppHostRemove("discord", "https://Discord.media/"); err != nil {
		t.Fatal(err)
	}
	a, err := o.App("discord")
	if err != nil || !a.User || a.Preset == nil || !reflect.DeepEqual(a.UDP, shipped.UDP) ||
		!slices.Equal(a.Hosts, slices.DeleteFunc(slices.Clone(shipped.Hosts), func(h string) bool { return h == "discord.media" })) {
		t.Fatalf("edited discord: %+v, %v", a, err)
	}
	if err := o.AppReset("discord"); err != nil {
		t.Fatal(err)
	}
	if a, _ := o.App("discord"); !reflect.DeepEqual(a, shipped) {
		t.Errorf("after reset: %+v, want the shipped app", a)
	}
}

func TestAppNewOwn(t *testing.T) {
	o := &Ops{P: paths.Under(t.TempDir())}
	if err := o.AppNew("steam", "Steam"); err != nil {
		t.Fatal(err)
	}
	if err := o.AppNew("steam", "Steam"); !errors.As(err, new(AppExistsError)) {
		t.Errorf("second new: %v", err)
	}
	if d, err := o.AppHostAdd("steam", "https://Store.SteamPowered.com/app/1"); err != nil || d != "store.steampowered.com" {
		t.Fatalf("add-host: %q, %v", d, err)
	}
	a, err := o.App("steam")
	c, _ := config.Load(o.P)
	if err != nil || !a.User || a.Preset != nil || a.Name != "Steam" || !slices.Equal(a.Hosts, []string{"store.steampowered.com"}) || !slices.Contains(c.Apps, "steam") {
		t.Fatalf("own app %+v, %v; enabled %q", a, err, c.Apps)
	}
	if err := o.AppReset("steam"); err != nil {
		t.Fatal(err)
	}
	c, _ = config.Load(o.P)
	if _, err := o.App("steam"); !errors.As(err, new(UnknownAppError)) || slices.Contains(c.Apps, "steam") {
		t.Errorf("after delete: %v; enabled %q", err, c.Apps)
	}
}

func TestNewAppID(t *testing.T) {
	taken := func(id string) bool { return id == "discord" || id == "discord-2" }
	for name, want := range map[string]string{
		"Steam Store":  "steam-store",
		" Şişli Çay! ": "sisli-cay",
		"İstanbul":     "istanbul",
		"Дискорд":      "app",
		"Discord":      "discord-3",
	} {
		if got := NewAppID(name, taken); got != want {
			t.Errorf("NewAppID(%q) = %q, want %q", name, got, want)
		}
	}
}
