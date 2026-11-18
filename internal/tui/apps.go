package tui

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"os/user"
	"path/filepath"
	"slices"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/KadirBerkpolat1/SplitDPI/internal/config"
	"github.com/KadirBerkpolat1/SplitDPI/internal/i18n"
	"github.com/KadirBerkpolat1/SplitDPI/internal/ops"
	"github.com/KadirBerkpolat1/SplitDPI/internal/presets"
	"github.com/KadirBerkpolat1/SplitDPI/internal/probe"
)

// The sites page in three parts: the apps to turn on or off, the user's own
// sites (`splitdpi sites`), and the sites the bypass never touches
// (`splitdpi exclude`).
type sitesPage struct{ noClose }

type appsScreen struct {
	apps     []presets.App
	sites    []string
	excluded []string
	cursor   int // over apps, then sites, then excluded sites
	input    textinput.Model
	typing   bool
	ask      askKind
	skipped  []ops.ImportSkip // lines the last import left out
	edit     *appEditor       // the app whose addresses are open, if any
}

// askKind is what the text input asks for.
type askKind int

const (
	askExclude askKind = iota
	askSite
	askFile
	askApp  // a new app's name
	askHost // an address for the open app
)

// rowKind is which part of the list a row is in.
type rowKind int

const (
	rowApp rowKind = iota
	rowSite
	rowExcluded
)

type (
	appsToggledMsg struct {
		name string
		on   bool
		err  error
	}
	importedMsg struct {
		path    string
		added   int
		skipped []ops.ImportSkip
		list    []string
		err     error
	}
	excludedMsg struct {
		note string
		list []string
		err  error
	}
	sitesMsg struct {
		note string
		list []string
		err  error
	}
)

func (sitesPage) Title() string            { return i18n.T("shell.menu.sites") }
func (sitesPage) Hint() string             { return i18n.T("shell.hint.sites") }
func (sitesPage) Open(m *Model) tea.Cmd    { m.openApps(); return nil }
func (sitesPage) Actions(m Model) []action { return m.appsActions() }

func (sitesPage) Key(m Model, k tea.KeyMsg, _ string) (Model, tea.Cmd, bool) { return m.appsKey(k) }

func (sitesPage) Update(m Model, msg tea.Msg) (Model, tea.Cmd, bool) { return m.appsUpdate(msg) }

func (sitesPage) View(m Model, w, h int) string { return m.appsView(w, h) }

func (m *Model) openApps() {
	set := m.b.PresetsList()
	m.loadNames(set)
	a := appsScreen{input: m.newInput()}
	for _, id := range slices.Sorted(maps.Keys(set.Apps)) {
		a.apps = append(a.apps, set.Apps[id])
	}
	// Enabled apps first; a toggle keeps its row until the page is opened again.
	off := func(app presets.App) int {
		if m.appEnabled(app.ID) {
			return 0
		}
		return 1
	}
	slices.SortStableFunc(a.apps, func(x, y presets.App) int { return off(x) - off(y) })
	if list, err := m.b.SitesList(); err == nil {
		a.sites = list
	} else {
		m.note = errText(err)
	}
	if list, err := m.b.ExcludeList(); err == nil {
		a.excluded = list
	} else {
		m.note = errText(err)
	}
	m.apps = a
}

func (m Model) appEnabled(id string) bool { return slices.Contains(m.cfg.Apps, id) }

func (a appsScreen) rows() int { return len(a.apps) + len(a.sites) + len(a.excluded) }

// at is the part of the list row i is in, and its index there.
func (a appsScreen) at(i int) (rowKind, int) {
	switch {
	case i < len(a.apps):
		return rowApp, i
	case i < len(a.apps)+len(a.sites):
		return rowSite, i - len(a.apps)
	}
	return rowExcluded, i - len(a.apps) - len(a.sites)
}

// site returns the user site under the cursor, if the cursor is on one.
func (a appsScreen) site() (string, bool) {
	if k, i := a.at(a.cursor); k == rowSite {
		return a.sites[i], true
	}
	return "", false
}

// excludedSite returns the excluded site under the cursor, if any.
func (a appsScreen) excludedSite() (string, bool) {
	if k, i := a.at(a.cursor); k == rowExcluded && i < len(a.excluded) {
		return a.excluded[i], true
	}
	return "", false
}

// appsActions: the row's own keys first, then adding.
func (m Model) appsActions() []action {
	a := m.apps
	cancel := action{Key: "esc", Label: i18n.T("tui.act.cancel"), Do: func(m *Model) tea.Cmd { return nil }} // appsKey cancels
	switch {
	case a.typing && a.ask == askSite:
		return []action{{Key: "enter", Label: i18n.T("tui.apps_screen.add"), CLI: "sudo splitdpi sites add " + a.input.Value(), Do: (*Model).siteTyped}, cancel}
	case a.typing && a.ask == askFile:
		return []action{{Key: "enter", Label: i18n.T("sites_import_tui.import"), CLI: "sudo splitdpi sites import " + a.input.Value(), Do: (*Model).importTyped}, cancel}
	case a.typing && a.ask == askApp:
		name := strings.TrimSpace(a.input.Value())
		return []action{{Key: "enter", Label: i18n.T("app_edit.create"), CLI: "sudo splitdpi apps new " + a.newID(name) + " " + shellArg(name), Do: (*Model).appTyped}, cancel}
	case a.typing && a.ask == askHost:
		return []action{{Key: "enter", Label: i18n.T("tui.apps_screen.add"), CLI: "sudo splitdpi apps add-host " + a.edit.id + " " + a.input.Value(), Do: (*Model).hostTyped}, cancel}
	case a.typing:
		return []action{{Key: "enter", Label: i18n.T("tui.apps_screen.exclude"), CLI: "sudo splitdpi exclude add " + a.input.Value(), Do: (*Model).excludeTyped}, cancel}
	case a.edit != nil:
		return m.appEditActions()
	}
	var own []action
	switch k, i := a.at(a.cursor); {
	case k == rowApp && i < len(a.apps):
		id := a.apps[i].ID
		verb := "enable"
		if m.appEnabled(id) {
			verb = "disable"
		}
		own = []action{
			{Key: "space", Label: i18n.T("tui.act.toggle"), CLI: "sudo splitdpi apps " + verb + " " + id, Do: (*Model).toggleApp},
			{Key: "o", Label: i18n.T("app_edit.open"), CLI: "sudo splitdpi apps show " + id, Do: (*Model).openAppEditor},
		}
	case k == rowSite:
		own = []action{{Key: "x", Label: i18n.T("tui.act.remove"), CLI: "sudo splitdpi sites remove " + a.sites[i], Do: (*Model).removeSite}}
	case k == rowExcluded && i < len(a.excluded):
		own = []action{{Key: "x", Label: i18n.T("tui.act.remove"), CLI: "sudo splitdpi exclude remove " + a.excluded[i], Do: (*Model).removeExcluded}}
	}
	return append(own,
		action{Key: "a", Label: i18n.T("tui.act.add_site"), CLI: "sudo splitdpi sites add DOMAIN", Do: startTyping(askSite)},
		action{Key: "e", Label: i18n.T("tui.apps_screen.site_broke"), CLI: "sudo splitdpi exclude add DOMAIN", Do: startTyping(askExclude)},
		action{Key: "i", Label: i18n.T("sites_import_tui.act"), CLI: "sudo splitdpi sites import FILE", Do: startTyping(askFile)},
		action{Key: "n", Label: i18n.T("app_edit.new"), CLI: "sudo splitdpi apps new ID NAME", Do: startTyping(askApp)},
	)
}

// startTyping opens the text input for k.
func startTyping(k askKind) func(m *Model) tea.Cmd {
	return func(m *Model) tea.Cmd {
		a := &m.apps
		a.typing, a.ask, a.skipped = true, k, nil
		a.input.Placeholder, a.input.CharLimit = "example.com", 253
		switch k {
		case askFile:
			a.input.Placeholder, a.input.CharLimit = i18n.T("sites_import_tui.placeholder"), 4096
		case askApp:
			a.input.Placeholder, a.input.CharLimit = "Steam", 64
		}
		a.input.SetValue("")
		return a.input.Focus()
	}
}

// importTyped adds the sites in the typed file; skipped lines also go to the log.
func (m *Model) importTyped() tea.Cmd {
	typed, b, w := strings.TrimSpace(m.apps.input.Value()), m.b, m.logs
	if typed == "" {
		return nil
	}
	m.apps.typing = false
	m.apps.input.Blur()
	path := expandHome(typed)
	return func() tea.Msg {
		added, skipped, err := b.SitesImport(path)
		if err != nil {
			fmt.Fprintln(w, err.Error())
			return importedMsg{path: path, err: err}
		}
		for _, s := range skipped {
			fmt.Fprintf(w, "%s:%d: %s\n", path, s.Line, s.Text)
		}
		list, err := b.SitesList()
		return importedMsg{path, len(added), skipped, list, err}
	}
}

// expandHome turns a leading ~/ into the home of the user who ran sudo, or
// else of this user.
func expandHome(p string) string {
	rest, ok := strings.CutPrefix(p, "~/")
	if !ok {
		return p
	}
	home, err := os.UserHomeDir()
	if name := os.Getenv("SUDO_USER"); name != "" {
		if u, uerr := user.Lookup(name); uerr == nil {
			home, err = u.HomeDir, nil
		}
	}
	if err != nil {
		return p
	}
	return filepath.Join(home, rest)
}

func (m *Model) toggleApp() tea.Cmd {
	k, i := m.apps.at(m.apps.cursor)
	if k != rowApp {
		return nil
	}
	app := m.apps.apps[i]
	on, b := !m.appEnabled(app.ID), m.b
	return func() tea.Msg {
		if on {
			return appsToggledMsg{app.Name, on, b.AppsEnable(app.ID)}
		}
		return appsToggledMsg{app.Name, on, b.AppsDisable(app.ID)}
	}
}

func (m *Model) excludeTyped() tea.Cmd {
	typed, b := strings.TrimSpace(m.apps.input.Value()), m.b
	if typed == "" {
		return nil
	}
	m.apps.typing = false
	m.apps.input.Blur()
	return func() tea.Msg {
		d, err := b.ExcludeAdd(typed)
		if err != nil {
			return excludedMsg{err: err}
		}
		list, err := b.ExcludeList()
		return excludedMsg{i18n.T("tui.note.excluded", d), list, err}
	}
}

func (m *Model) removeExcluded() tea.Cmd {
	d, ok := m.apps.excludedSite()
	if !ok {
		return nil
	}
	b := m.b
	return func() tea.Msg {
		if err := b.ExcludeRemove(d); err != nil {
			return excludedMsg{err: err}
		}
		list, err := b.ExcludeList()
		return excludedMsg{i18n.T("tui.note.unexcluded", d), list, err}
	}
}

func (m *Model) siteTyped() tea.Cmd {
	typed, b := strings.TrimSpace(m.apps.input.Value()), m.b
	if typed == "" {
		return nil
	}
	m.apps.typing = false
	m.apps.input.Blur()
	return func() tea.Msg {
		d, err := b.SitesAdd(typed)
		if err != nil {
			return sitesMsg{err: err}
		}
		list, err := b.SitesList()
		return sitesMsg{i18n.T("tui.note.site_added", d), list, err}
	}
}

func (m *Model) removeSite() tea.Cmd {
	d, ok := m.apps.site()
	if !ok {
		return nil
	}
	b := m.b
	return func() tea.Msg {
		if err := b.SitesRemove(d); err != nil {
			return sitesMsg{err: err}
		}
		list, err := b.SitesList()
		return sitesMsg{i18n.T("tui.note.site_removed", d), list, err}
	}
}

func (m Model) appsUpdate(msg tea.Msg) (Model, tea.Cmd, bool) {
	a := &m.apps
	switch msg := msg.(type) {
	case appsToggledMsg:
		if msg.err != nil {
			m.note = errText(msg.err)
			return m, m.refresh(), true
		}
		note := i18n.T("tui.note.app_off", msg.name)
		if msg.on {
			note = i18n.T("tui.note.app_on", msg.name)
		}
		m.note = m.withChange(note)
		return m, m.refresh(), true
	case excludedMsg:
		if msg.err != nil {
			m.note = errText(msg.err)
			return m, nil, true
		}
		a.excluded = msg.list
		a.cursor = max(min(a.cursor, a.rows()-1), 0)
		m.note = m.withChange(msg.note)
		return m, nil, true
	case sitesMsg:
		if msg.err != nil {
			m.note = errText(msg.err)
			return m, nil, true
		}
		a.sites = msg.list
		a.cursor = max(min(a.cursor, a.rows()-1), 0)
		m.note = m.withChange(msg.note)
		return m, nil, true
	case importedMsg:
		switch {
		case errors.Is(msg.err, fs.ErrNotExist):
			m.note = i18n.T("sites_import_tui.not_found", msg.path)
			return m, nil, true
		case msg.err != nil:
			m.note = errText(msg.err)
			return m, nil, true
		}
		a.sites, a.skipped = msg.list, msg.skipped
		a.cursor = max(min(a.cursor, a.rows()-1), 0)
		note := []string{i18n.N("sites_import_tui.added", msg.added)}
		if n := len(msg.skipped); n > 0 {
			note = append(note, i18n.N("sites_import_tui.skipped", n))
		}
		m.note = strings.Join(note, " ")
		if msg.added > 0 {
			m.note = m.withChange(m.note)
		}
		return m, nil, true
	case appEditedMsg:
		return m.appEdited(msg)
	}
	return m, nil, false
}

// appsKey feeds the text input while typing and moves the cursor over the
// rows of all three parts.
func (m Model) appsKey(k tea.KeyMsg) (Model, tea.Cmd, bool) {
	a := &m.apps
	if a.typing {
		switch k.String() {
		case "enter":
			return m, nil, false // the add or exclude action submits
		case "esc":
			a.typing = false
			a.input.Blur()
			return m, nil, true
		}
		var cmd tea.Cmd
		a.input, cmd = a.input.Update(k)
		return m, cmd, true
	}
	if a.edit != nil {
		return m.appEditKey(k)
	}
	n := a.rows()
	switch k.String() {
	case "up", "k":
		if n > 0 {
			a.cursor = (a.cursor + n - 1) % n
		}
		return m, nil, true
	case "down", "j":
		if n > 0 {
			a.cursor = (a.cursor + 1) % n
		}
		return m, nil, true
	}
	return m, nil, false
}

// appsView lists the three parts under their headings: apps (on or off),
// the user's sites, the sites never touched. Under the list: the selected
// row's note or failed probe, the lines the last import skipped, or the
// question while typing.
func (m Model) appsView(w, h int) string {
	a := m.apps
	if a.edit != nil {
		return m.appEditView(w, h)
	}
	var lines []string
	first := make([]int, 0, a.rows()) // each row's line
	nameW := 0
	for _, app := range a.apps {
		nameW = max(nameW, lipgloss.Width(app.Name))
	}
	for _, d := range slices.Concat(a.sites, a.excluded) {
		nameW = max(nameW, lipgloss.Width(d))
	}
	nameW = min(nameW, w/2)
	name := lipgloss.NewStyle().Width(nameW).MaxWidth(nameW)
	entry := func(i int, mark, label, what string) {
		first = append(first, len(lines))
		head := m.listMark(i == a.cursor && !a.typing) + mark + " " + name.Render(label)
		if what != "" {
			head += "  " + what
		}
		lines = append(lines, head)
	}
	section := func(title string, n int, empty string) {
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, groupStyle.Render(title)+mutedStyle.Render(fmt.Sprintf("  %d", n)))
		if n == 0 {
			lines = append(lines, "  "+mutedStyle.Render(empty))
		}
	}

	section(i18n.T("shell.sites.apps"), len(a.apps), i18n.T("shell.sites.apps_empty"))
	for i, app := range a.apps {
		entry(i, m.mark(m.appEnabled(app.ID)), app.Name, m.rowWhat(appKind(app), m.probe[app.ID]))
	}
	section(i18n.T("shell.sites.yours"), len(a.sites), i18n.T("shell.sites.yours_empty"))
	for i, d := range a.sites {
		label := d
		if probe.Overall(m.probe[d]) == probe.NoAddress {
			label = mutedStyle.Render(d)
		}
		entry(len(a.apps)+i, goodStyle.Render(m.g.on), label, m.rowWhat("", m.probe[d]))
	}
	section(i18n.T("shell.sites.excluded"), len(a.excluded), i18n.T("shell.sites.excluded_empty"))
	for i, d := range a.excluded {
		entry(len(a.apps)+len(a.sites)+i, warnStyle.Render(m.g.never), d, "")
	}

	var head []string
	if m.mode() == config.ModeBroad {
		head = []string{ansi.Truncate(mutedStyle.Render(i18n.T("shell.sites.broad")), w, m.g.ellipsis), ""}
	}
	var tail []string
	if a.typing {
		q := i18n.T("tui.apps_screen.which_site")
		switch a.ask {
		case askSite:
			q = i18n.T("tui.apps_screen.which_add")
		case askFile:
			q = i18n.T("sites_import_tui.which")
		case askApp:
			q = i18n.T("app_edit.which_app")
		}
		tail = append([]string{""}, strings.Split(lipgloss.NewStyle().Width(w).Render(q), "\n")...)
		tail = append(tail, ansi.Truncate(a.input.View(), w, ""))
	} else {
		if about := m.rowAbout(); about != "" {
			tail = append([]string{""}, strings.Split(mutedStyle.Width(w).Render(about), "\n")...)
		}
		// The skipped lines take what the list leaves, so that their count stays.
		if left := h - len(head) - min(5, len(lines)) - len(tail) - 1; left >= 2 {
			if sk := a.skippedView(w, min(max(h/3, 3), left)); len(sk) > 0 {
				tail = append(append(tail, ""), sk...)
			}
		}
	}
	// The list keeps a few lines; what is under it gives way.
	room := max(h-len(head)-len(tail), min(5, len(lines)))
	tail = tail[:min(len(tail), max(h-len(head)-room, 0))]
	list := m.scrollMore(lines, first, a.cursor, room, w)
	return strings.Join(slices.Concat(head, list, tail), "\n")
}

// rowWhat is what follows a row's name: its kind, and why its probe failed.
func (m Model) rowWhat(kind string, rs []probe.Result) string {
	parts := []string{}
	if kind != "" {
		parts = append(parts, mutedStyle.Render(kind))
	}
	switch c := probe.Overall(rs); {
	case c == probe.NoAddress:
		parts = append(parts, mutedStyle.Render(c.What()))
	case c.Failed():
		parts = append(parts, badStyle.Render(i18n.T("probe_short."+string(c))))
	}
	return strings.Join(parts, mutedStyle.Render(", "))
}

// rowAbout is what the selected row needs said: what to do about its
// failed probe, an app's coverage note, or what a never-touched site is.
func (m Model) rowAbout() string {
	a := m.apps
	switch k, i := a.at(a.cursor); k {
	case rowApp:
		if i >= len(a.apps) {
			return ""
		}
		app := a.apps[i]
		if c := probe.Overall(m.probe[app.ID]); c != probe.None {
			return probeTodo(c)
		}
		return ops.AppNote(app)
	case rowSite:
		if c := probe.Overall(m.probe[a.sites[i]]); c != probe.None {
			return probeTodo(c)
		}
	case rowExcluded:
		return i18n.T("shell.sites.excluded_about")
	}
	return ""
}

// skippedView lists the lines the last import skipped in at most room lines.
func (a appsScreen) skippedView(w, room int) []string {
	if len(a.skipped) == 0 {
		return nil
	}
	lines := []string{warnStyle.Render(i18n.T("sites_import_tui.skipped_title"))}
	shown := a.skipped
	if len(shown) > room-1 {
		shown = shown[:max(room-2, 0)]
	}
	for _, s := range shown {
		lines = append(lines, "  "+i18n.T("sites_import_tui.skipped_line", s.Line, s.Text))
	}
	if n := len(a.skipped) - len(shown); n > 0 {
		lines = append(lines, mutedStyle.Render("  "+i18n.T("sites_import_tui.more", n)))
	}
	for i, l := range lines {
		lines[i] = ansi.Truncate(l, w, "")
	}
	return lines
}
