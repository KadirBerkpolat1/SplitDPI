package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/KadirBerkpolat1/SplitDPI/internal/i18n"
	"github.com/KadirBerkpolat1/SplitDPI/internal/ops"
	"github.com/KadirBerkpolat1/SplitDPI/internal/presets"
)

// extraScreens are the screens pageViews does not reach.
var extraScreens = []struct {
	name string
	view func(t *testing.T, prep func(Model) Model) map[string]string
}{
	{"app editor", func(t *testing.T, prep func(Model) Model) map[string]string {
		f := newFake()
		f.set = trSet()
		f.set.Apps["discord"] = presets.App{ID: "discord", Name: "Discord", Hosts: []string{"discord.gg", "discord.media"}, UDP: &presets.UDP{Ports: "50000-50100"}}
		return map[string]string{"app editor": press(t, goTo(t, prep(newModel(t, f)), pgSites), "o").View()}
	}},
	// Enter on every settings row that opens something on the settings page.
	{"settings", func(t *testing.T, prep func(Model) Model) map[string]string {
		f := newFake()
		f.st.State = "running"
		f.excluded = []string{"bank.example"}
		f.foreign = []ops.Foreign{zapret2()}
		f.takenOver = []string{"zapret.service (systemd)"}
		f.set.Version = "2026.10.01"
		base := goTo(t, prep(newModel(t, f)), pgSettings)
		views := map[string]string{}
		for _, st := range base.shownSettings() {
			if m := press(t, toRow(t, base, st.id), "enter"); m.page == pgSettings {
				views["settings "+st.id] = m.View()
			}
		}
		return views
	}},
	{"scan dns", func(t *testing.T, prep func(Model) Model) map[string]string {
		m, done, _ := dnsScan(t, newFake())
		m = prep(waitAsk(t, m))
		views := map[string]string{"scan dns question": m.View()}
		m = press(t, press(t, m, "down"), "enter")
		views["scan dns providers"] = m.View()
		m = press(t, m, "esc")
		m = press(t, m, "esc")
		m.cancelScan()
		<-done
		return views
	}},
}

var unsafeName = regexp.MustCompile(`[^a-z0-9]+`)

// TestDumpScreens writes every screen, true-color ANSI on a dark background,
// to $SPLITDPI_SCREENS_DIR as <lang>-<W>x<H>-<screen>.ans for
// scripts/tui-screens.sh.
func TestDumpScreens(t *testing.T) {
	dir := os.Getenv("SPLITDPI_SCREENS_DIR")
	if dir == "" {
		t.Skip("SPLITDPI_SCREENS_DIR is not set")
	}
	profile, dark := lipgloss.ColorProfile(), lipgloss.HasDarkBackground()
	lipgloss.SetColorProfile(termenv.TrueColor)
	lipgloss.SetHasDarkBackground(true)
	defer func() {
		lipgloss.SetColorProfile(profile)
		lipgloss.SetHasDarkBackground(dark)
		i18n.Init("en")
	}()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, run := range []struct {
		lang string
		w, h int
	}{{"tr", 80, 24}, {"tr", 100, 30}, {"en", 100, 30}} {
		size := fmt.Sprintf("%s-%dx%d", run.lang, run.w, run.h)
		i18n.Init(run.lang)
		prep := func(m Model) Model {
			m.width, m.height = run.w, run.h
			return m
		}
		views := pageViews(t, prep)
		for _, x := range extraScreens {
			t.Run(size+" "+x.name, func(t *testing.T) {
				for name, v := range x.view(t, prep) {
					views[name] = v
				}
			})
		}
		for name, v := range views {
			file := size + "-" + strings.Trim(unsafeName.ReplaceAllString(strings.ToLower(name), "-"), "-") + ".ans"
			if err := os.WriteFile(filepath.Join(dir, file), []byte(v+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		t.Logf("%s: %d screens", size, len(views))
	}
}
