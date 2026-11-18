package tui

import (
	"maps"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/KadirBerkpolat1/SplitDPI/internal/i18n"
	"github.com/KadirBerkpolat1/SplitDPI/internal/ops"
	"github.com/KadirBerkpolat1/SplitDPI/internal/presets"
)

// The app editor of the Sites page: an app's addresses. A change saves the
// user's copy of the app, and reset drops it (`splitdpi apps add-host`,
// `remove-host`, `reset`).
type appEditor struct {
	id     string
	cursor int // over the addresses
}

type appEditedMsg struct {
	open string // the app to edit next, if any
	note string
	err  error
}

// appKind is what the Sites list says of an app the user changed or added;
// nothing for a shipped one.
func appKind(app presets.App) string {
	switch {
	case app.User && app.Preset != nil:
		return i18n.T("shell.sites.edited")
	case app.User:
		return i18n.T("shell.sites.own")
	}
	return ""
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
	if k, i := m.apps.at(m.apps.cursor); k == rowApp && i < len(m.apps.apps) {
		m.apps.edit = &appEditor{id: m.apps.apps[i].ID}
	}
	return nil
}

func (m Model) appEditActions() []action {
	back := action{Key: "esc", Label: i18n.T("tui.act.back"), Do: func(m *Model) tea.Cmd { m.apps.edit = nil; return nil }}
	app, ok := m.apps.edited()
	if !ok {
		return []action{back}
	}
	id := app.ID
	acts := []action{{Key: "a", Label: i18n.T("app_edit.add"), CLI: "sudo splitdpi apps add-host " + id + " DOMAIN", Do: startTyping(askHost)}}
	if cur := m.apps.edit.cursor; cur < len(app.Hosts) {
		h := app.Hosts[cur]
		acts = append(acts, action{Key: "x", Label: i18n.T("tui.act.remove"), CLI: "sudo splitdpi apps remove-host " + id + " " + h, Do: func(m *Model) tea.Cmd {
			return m.appChange("", i18n.T("tui.note.host_removed", h, app.Name), func(b Backend) error { return b.AppHostRemove(id, h) })
		}})
	}
	if app.User {
		label, note := i18n.T("app_edit.reset"), i18n.T("tui.note.app_reset", app.Name)
		if app.Preset == nil {
			label, note = i18n.T("app_edit.delete"), i18n.T("tui.note.app_deleted", app.Name)
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
	name := id
	if app, ok := m.apps.edited(); ok {
		name = app.Name
	}
	m.apps.typing = false
	m.apps.input.Blur()
	return func() tea.Msg {
		d, err := b.AppHostAdd(id, typed)
		if err != nil {
			return appEditedMsg{err: err}
		}
		return appEditedMsg{note: i18n.T("tui.note.host_added", d, name)}
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
	return m.appChange(id, i18n.T("tui.note.app_created", name), func(b Backend) error { return b.AppNew(id, name) })
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
	m.loadNames(set)
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
		a.edit = &appEditor{id: msg.open}
	}
	if app, ok := a.edited(); ok {
		e := *a.edit
		e.cursor = max(min(e.cursor, len(app.Hosts)-1), 0)
		a.edit = &e
	} else {
		a.edit = nil
	}
	a.cursor = max(min(a.cursor, a.rows()-1), 0)
	m.note = msg.note
	if msg.open == "" {
		m.note = m.withChange(msg.note)
	}
	return m, m.refresh(), true
}

// appEditKey moves the editor's cursor; esc closes the editor.
func (m Model) appEditKey(k tea.KeyMsg) (Model, tea.Cmd, bool) {
	a := &m.apps
	app, _ := a.edited()
	n, e := len(app.Hosts), *a.edit
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
		a.edit = nil
		return m, nil, true
	default:
		return m, nil, false
	}
	a.edit = &e
	return m, nil, true
}

// appEditView lists the app's addresses; under them the question while
// typing an address.
func (m Model) appEditView(w, h int) string {
	a := m.apps
	app, _ := a.edited()
	wrap := lipgloss.NewStyle().Width(w)
	title := groupStyle.Render(app.Name)
	if kind := appKind(app); kind != "" {
		title += mutedStyle.Render("  " + kind)
	}
	head := []string{title}
	head = append(head, strings.Split(mutedStyle.Width(w).Render(i18n.T("app_edit.head")), "\n")...)
	head = append(head, "")
	var lines []string
	for i, host := range app.Hosts {
		lines = append(lines, m.listMark(i == a.edit.cursor && !a.typing)+host)
	}
	if len(app.Hosts) == 0 {
		lines = append(lines, mutedStyle.Render(i18n.T("app_edit.empty")))
	}
	var tail []string
	if a.typing {
		tail = append([]string{""}, strings.Split(wrap.Render(i18n.T("app_edit.which_host")), "\n")...)
		tail = append(tail, ansi.Truncate(a.input.View(), w, ""))
	}
	out := append(head, m.scrollMore(lines, nil, a.edit.cursor, h-len(head)-len(tail), w)...)
	return strings.Join(append(out, tail...), "\n")
}
