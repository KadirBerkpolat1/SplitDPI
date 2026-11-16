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
	f.set.Apps["discord"] = presets.App{ID: "discord", Name: "Discord", Hosts: []string{"discord.gg", "discord.media"}, UDP: &presets.UDP{Ports: "50000-50100"}}
	m := goTo(t, newModel(t, f), pgSites)
	m = press(t, m, "o")
	has(t, m.View(), "discord.media")
	has(t, m.View(), "Voice and streams (UDP)")

	m = press(t, press(t, m, "down"), "x")
	if !f.called("apps remove-host discord discord.media") {
		t.Fatalf("calls %q", f.calls)
	}
	if app, _ := m.apps.edited(); slices.Contains(app.Hosts, "discord.media") {
		t.Error("the removed address is still listed")
	}
	has(t, m.View(), "app, edited by you")
	// The cursor stays on the second row, now the voice row.
	has(t, m.View(), "decoy packets")
	has(t, m.View(), "$ sudo splitdpi apps udp discord off")
	m = press(t, m, "v")
	if !f.called("apps udp discord off") || f.set.Apps["discord"].UDP != nil {
		t.Fatalf("voice not turned off: calls %q", f.calls)
	}
	has(t, m.View(), "$ sudo splitdpi apps udp discord on")

	m = press(t, m, "r")
	if !f.called("apps reset discord") {
		t.Fatalf("calls %q", f.calls)
	}
	has(t, m.View(), "discord.media")
	if strings.Contains(m.View(), "edited by you") {
		t.Error("a reset app still shows as edited")
	}
	m = press(t, m, "esc")
	if m.apps.edit != nil {
		t.Fatal("esc did not close the editor")
	}

	m = press(t, m, "n")
	m = typeText(t, m, "Steam Store")
	has(t, m.View(), "$ sudo splitdpi apps new steam-store 'Steam Store'")
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
