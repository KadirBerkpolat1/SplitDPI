package tui

import (
	"slices"
	"strings"
	"testing"

	"github.com/KadirBerkpolat1/SplitDPI/internal/presets"
)

func TestAppEditor(t *testing.T) {
	f := newFake()
	f.set = trSet()
	f.set.Apps["discord"] = presets.App{ID: "discord", Name: "Discord", Hosts: []string{"discord.gg", "discordapp.net"}, UDP: &presets.UDP{Ports: "50000-50100"}}
	m := goTo(t, newModel(t, f), pgSites)
	m = press(t, m, "o")
	has(t, m.View(), "discordapp.net")
	if strings.Contains(m.View(), "UDP") {
		t.Errorf("the editor offers the voice profile:\n%s", m.View())
	}
	if bar, _ := m.hintBar(); strings.Contains(bar, "v ") {
		t.Errorf("hint bar %q offers v", bar)
	}

	m = press(t, press(t, m, "down"), "x")
	if !f.called("apps remove-host discord discordapp.net") {
		t.Fatalf("calls %q", f.calls)
	}
	if app, _ := m.apps.edited(); slices.Contains(app.Hosts, "discordapp.net") {
		t.Error("the removed address is still listed")
	}
	has(t, m.View(), "changed by you")
	if app, _ := m.apps.edited(); app.UDP == nil {
		t.Error("an address edit dropped the app's voice profile")
	}

	m = press(t, m, "r")
	if !f.called("apps reset discord") {
		t.Fatalf("calls %q", f.calls)
	}
	has(t, m.View(), "discordapp.net")
	if strings.Contains(m.View(), "changed by you") {
		t.Error("a reset app still shows as edited")
	}
	m = press(t, m, "esc")
	if m.apps.edit != nil || m.page != pgSites {
		t.Fatal("esc did not close the editor")
	}

	m = press(t, m, "n")
	m = typeText(t, m, "Steam Store")
	if cli := cliFor(m, "enter"); cli != "sudo splitdpi apps new steam-store 'Steam Store'" {
		t.Errorf("new app CLI %q", cli)
	}
	m = press(t, m, "enter")
	if !f.called("apps new steam-store Steam Store") {
		t.Fatalf("calls %q", f.calls)
	}
	if m.apps.edit == nil || m.apps.edit.id != "steam-store" {
		t.Fatalf("the new app is not open: %+v", m.apps.edit)
	}
	has(t, m.View(), "No addresses yet.")
	m = press(t, m, "a")
	m = typeText(t, m, "store.steampowered.com")
	m = press(t, m, "enter")
	if !f.called("apps add-host steam-store store.steampowered.com") {
		t.Fatalf("calls %q", f.calls)
	}
	has(t, m.View(), "store.steampowered.com")
	m = press(t, m, "r")
	if _, ok := f.set.Apps["steam-store"]; ok || m.apps.edit != nil || m.apps.has("steam-store") {
		t.Errorf("delete left the app: editor %+v", m.apps.edit)
	}
}
