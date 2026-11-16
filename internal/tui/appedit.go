package tui

import (
	"maps"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/KadirBerkpolat1/SplitDPI/internal/i18n"
	"github.com/KadirBerkpolat1/SplitDPI/internal/ops"
	"github.com/KadirBerkpolat1/SplitDPI/internal/presets"
)

// The app editor of the Sites page: an app's addresses and its voice and
// stream (UDP) profile. A change saves the user's copy of the app, and reset
// drops it (`splitdpi apps add-host`, `remove-host`, `udp`, `reset`).
type appEditor struct {
	id     string
	cursor int // over the addresses, then the voice row if the app has one
}

type appEditedMsg struct {
	open string // the app to edit next, if any
	note string
	err  error
}

// appKind is what the Sites list says an app is.
func appKind(app presets.App) string {
	switch {
	case app.User && app.Preset != nil:
		return i18n.T("shell.sites.app_edited")
	case app.User:
		return i18n.T("shell.sites.app_yours")
	}
	return i18n.T("shell.sites.app")
}

// voice reports whether the app has a voice and stream profile to turn on or off.
func voice(app presets.App) bool {
	return app.UDP != nil || app.Preset != nil && app.Preset.UDP != nil
}

func editRows(app presets.App) int {
	if voice(app) {
		return len(app.Hosts) + 1
	}
	return len(app.Hosts)
}

func (a appsScreen) has(id string) bool {
	return slices.ContainsFunc(a.apps, func(x presets.App) bool { return x.ID == id })
}

// newID is the id a new app called name gets.
func (a appsScreen) newID(name string) string { return ops.NewAppID(name, a.has) }

// edited returns the app the editor shows.
func (a appsScreen) edited() (presets.App, bool) {
	if a.edit == nil {
		return presets.App{}, false
	}
	i := slices.IndexFunc(a.apps, func(x presets.App) bool { return x.ID == a.edit.id })
	if i < 0 {
		return presets.App{}, false
	}
	return a.apps[i], true
}

func (m *Model) openAppEditor() tea.Cmd {
	a := &m.apps
	if a.cursor < len(a.apps) {
		a.edit, m.focus = &appEditor{id: a.apps[a.cursor].ID}, 0
	}
	return nil
}

func (m Model) appEditActions() []action {
	back := action{Key: "esc", Label: i18n.T("tui.act.back"), Do: func(m *Model) tea.Cmd { m.apps.edit, m.focus = nil, 0; return nil }}
	app, ok := m.apps.edited()
	if !ok {
		return []action{back}
	}
	id := app.ID
	acts := []action{{Key: "a", Label: i18n.T("app_edit.add"), CLI: "sudo splitdpi apps add-host " + id + " DOMAIN", Do: startTyping(askHost)}}
	if cur := m.apps.edit.cursor; cur < len(app.Hosts) {
		h := app.Hosts[cur]
		acts = append(acts, action{Key: "x", Label: i18n.T("tui.act.remove"), CLI: "sudo splitdpi apps remove-host " + id + " " + h, Do: func(m *Model) tea.Cmd {
			return m.appChange("", i18n.T("apps.host_removed", h, id), func(b Backend) error { return b.AppHostRemove(id, h) })
		}})
	}
	if voice(app) {
		on, arg, note := app.UDP == nil, "off", i18n.T("apps.udp_off", id)
		if on {
			arg, note = "on", i18n.T("apps.udp_on", id)
		}
		acts = append(acts, action{Key: "v", Label: i18n.T("app_edit.voice_toggle"), CLI: "sudo splitdpi apps udp " + id + " " + arg, Do: func(m *Model) tea.Cmd {
			return m.appChange("", note, func(b Backend) error { return b.AppSetUDP(id, on) })
		}})
	}
	if app.User {
		label, note := i18n.T("app_edit.reset"), i18n.T("apps.reset", id)
		if app.Preset == nil {
			label, note = i18n.T("app_edit.delete"), i18n.T("apps.deleted", id)
		}
		acts = append(acts, action{Key: "r", Label: label, CLI: "sudo splitdpi apps reset " + id, Do: func(m *Model) tea.Cmd {
			return m.appChange("", note, func(b Backend) error { return b.AppReset(id) })
		}})
	}
	return append(acts, back)
}

// appChange runs change off the UI thread; open names the app to edit after it.
func (m *Model) appChange(open, note string, change func(b Backend) error) tea.Cmd {
	b := m.b
	return func() tea.Msg {
		if err := change(b); err != nil {
			return appEditedMsg{err: err}
		}
		return appEditedMsg{open: open, note: note}
	}
}

func (m *Model) hostTyped() tea.Cmd {
	typed, b, id := strings.TrimSpace(m.apps.input.Value()), m.b, m.apps.edit.id
	if typed == "" {
		return nil
	}
	m.apps.typing = false
	m.apps.input.Blur()
	return func() tea.Msg {
		d, err := b.AppHostAdd(id, typed)
		if err != nil {
			return appEditedMsg{err: err}
		}
		return appEditedMsg{note: i18n.T("apps.host_added", d, id)}
	}
}

// appTyped creates the app and opens it, so that its addresses come next.
func (m *Model) appTyped() tea.Cmd {
	name := strings.TrimSpace(m.apps.input.Value())
	if name == "" {
		return nil
	}
	m.apps.typing = false
	m.apps.input.Blur()
	id := m.apps.newID(name)
	return m.appChange(id, i18n.T("apps.created", id), func(b Backend) error { return b.AppNew(id, name) })
}

// appEdited reads the apps again in their order, a new one last, and keeps
// the editor on its app while it exists.
func (m Model) appEdited(msg appEditedMsg) (Model, tea.Cmd, bool) {
	if msg.err != nil {
		m.note = errText(msg.err)
		return m, nil, true
	}
	a := &m.apps
	set := m.b.PresetsList()
	apps := make([]presets.App, 0, len(set.Apps))
	for _, old := range a.apps {
		if app, ok := set.Apps[old.ID]; ok {
			apps = append(apps, app)
		}
	}
	for _, id := range slices.Sorted(maps.Keys(set.Apps)) {
		if !a.has(id) {
			apps = append(apps, set.Apps[id])
		}
	}
	a.apps = apps
	if msg.open != "" {
		a.edit, m.focus = &appEditor{id: msg.open}, 0
	}
	if app, ok := a.edited(); ok {
		e := *a.edit
		e.cursor = max(min(e.cursor, editRows(app)-1), 0)
		a.edit = &e
	} else {
		a.edit = nil
	}
	a.cursor = max(min(a.cursor, a.rows()-1), 0)
	m.note = msg.note
	return m, m.refresh(), true
}

// appEditKey moves the editor's cursor; esc closes the editor.
func (m Model) appEditKey(k tea.KeyMsg) (Model, tea.Cmd, bool) {
	a := &m.apps
	app, _ := a.edited()
	n, e := editRows(app), *a.edit
	switch k.String() {
	case "up", "k":
		if n > 0 {
			e.cursor = (e.cursor + n - 1) % n
		}
	case "down", "j":
		if n > 0 {
			e.cursor = (e.cursor + 1) % n
		}
	case "esc":
		a.edit, m.focus = nil, 0
		return m, nil, true
	default:
		return m, nil, false
	}
	a.edit = &e
	return m, nil, true
}

// appEditView lists the app's addresses and, last, its voice and stream row;
// under them what that row does, or the question while typing an address.
func (m Model) appEditView(w, h int) string {
	a := m.apps
	app, _ := a.edited()
	wrap := lipgloss.NewStyle().Width(w)
	head := []string{titleStyle.Render(app.Name) + mutedStyle.Render("  "+appKind(app))}
	head = append(head, strings.Split(mutedStyle.Width(w).Render(i18n.T("app_edit.head")), "\n")...)
	head = append(head, "")
	cur := a.edit.cursor
	var lines []string
	for i, host := range app.Hosts {
		lines = append(lines, m.listMark(i == cur && !a.typing)+host)
	}
	if len(app.Hosts) == 0 {
		lines = append(lines, mutedStyle.Render(i18n.T("app_edit.empty")))
	}
	line := cur
	var tail []string
	if voice(app) {
		on := cur == len(app.Hosts)
		if on {
			line = len(lines)
		}
		lines = append(lines, m.listMark(on && !a.typing)+m.mark(app.UDP != nil)+" "+i18n.T("app_edit.voice"))
		if on && !a.typing {
			tail = append([]string{""}, strings.Split(mutedStyle.Width(w).Render(i18n.T("app_edit.voice_hint")), "\n")...)
		}
	}
	if a.typing {
		tail = append([]string{""}, strings.Split(wrap.Render(i18n.T("app_edit.which_host")), "\n")...)
		tail = append(tail, a.input.View())
	}
	out := append(head, scroll(lines, line, h-len(head)-len(tail), w)...)
	return strings.Join(append(out, tail...), "\n")
}
