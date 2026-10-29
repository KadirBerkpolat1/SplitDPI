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

	"github.com/KadirBerkpolat1/SplitDPI/internal/i18n"
	"github.com/KadirBerkpolat1/SplitDPI/internal/ops"
	"github.com/KadirBerkpolat1/SplitDPI/internal/presets"
	"github.com/KadirBerkpolat1/SplitDPI/internal/probe"
)

// The sites page: toggle apps, add or remove the user's own sites
// (`splitdpi sites`), and "a site broke" adds an exclusion
// (`splitdpi exclude add`).
type sitesPage struct{}

type appsScreen struct {
	apps     []presets.App
	sites    []string
	cursor   int // over apps, then sites
	excluded []string
	input    textinput.Model
	typing   bool
	ask      askKind
	skipped  []ops.ImportSkip // lines the last import left out
}

// askKind is what the text input asks for.
type askKind int

const (
	askExclude askKind = iota
	askSite
	askFile
)

type (
	appsToggledMsg struct{ err error }
	importedMsg    struct {
		path    string
		added   int
		skipped []ops.ImportSkip
		list    []string
		err     error
	}
	excludedMsg struct {
		domain string
		list   []string
		err    error
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
	a := appsScreen{input: textinput.New()}
	for _, id := range slices.Sorted(maps.Keys(set.Apps)) {
		a.apps = append(a.apps, set.Apps[id])
	}
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

func (a appsScreen) rows() int { return len(a.apps) + len(a.sites) }

// site returns the user site under the cursor, if the cursor is on one.
func (a appsScreen) site() (string, bool) {
	i := a.cursor - len(a.apps)
	if i < 0 || i >= len(a.sites) {
		return "", false
	}
	return a.sites[i], true
}

func (m Model) appsActions() []action {
	a := m.apps
	cancel := action{Key: "esc", Label: i18n.T("tui.act.cancel"), Do: func(m *Model) tea.Cmd { return nil }} // appsKey cancels
	switch {
	case a.typing && a.ask == askSite:
		return []action{{Key: "enter", Label: i18n.T("tui.apps_screen.add"), CLI: "sudo splitdpi sites add " + a.input.Value(), Do: (*Model).siteTyped}, cancel}
	case a.typing && a.ask == askFile:
		return []action{{Key: "enter", Label: i18n.T("sites_import_tui.import"), CLI: "sudo splitdpi sites import " + a.input.Value(), Do: (*Model).importTyped}, cancel}
	case a.typing:
		return []action{{Key: "enter", Label: i18n.T("tui.apps_screen.exclude"), CLI: "sudo splitdpi exclude add " + a.input.Value(), Do: (*Model).excludeTyped}, cancel}
	}
	// The second action follows the cursor, so focus keeps its place.
	first := action{Key: "space", Label: i18n.T("tui.act.toggle"), Do: (*Model).toggleApp}
	if d, ok := a.site(); ok {
		first = action{Key: "x", Label: i18n.T("tui.act.remove"), CLI: "sudo splitdpi sites remove " + d, Do: (*Model).removeSite}
	} else if a.cursor < len(a.apps) {
		verb := "enable"
		if m.appEnabled(a.apps[a.cursor].ID) {
			verb = "disable"
		}
		first.CLI = "sudo splitdpi apps " + verb + " " + a.apps[a.cursor].ID
	}
	startTyping := func(k askKind) func(m *Model) tea.Cmd {
		return func(m *Model) tea.Cmd {
			a := &m.apps
			a.typing, a.ask, a.skipped, m.focus, m.inPage = true, k, nil, 0, true
			a.input.Placeholder, a.input.CharLimit = "example.com", 253
			if k == askFile {
				a.input.Placeholder, a.input.CharLimit = i18n.T("sites_import_tui.placeholder"), 4096
			}
			a.input.SetValue("")
			return a.input.Focus()
		}
	}
	return []action{
		{Key: "a", Label: i18n.T("tui.act.add_site"), CLI: "sudo splitdpi sites add DOMAIN", Do: startTyping(askSite)},
		first,
		{Key: "e", Label: i18n.T("tui.apps_screen.site_broke"), CLI: "sudo splitdpi exclude add DOMAIN", Do: startTyping(askExclude)},
		{Key: "i", Label: i18n.T("sites_import_tui.act"), CLI: "sudo splitdpi sites import FILE", Do: startTyping(askFile)},
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
	a := m.apps
	if a.cursor >= len(a.apps) {
		return nil
	}
	id, on, b := a.apps[a.cursor].ID, m.appEnabled(a.apps[a.cursor].ID), m.b
	return func() tea.Msg {
		if on {
			return appsToggledMsg{b.AppsDisable(id)}
		}
		return appsToggledMsg{b.AppsEnable(id)}
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
		return excludedMsg{d, list, err}
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
		return sitesMsg{i18n.T("sites.added", d), list, err}
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
		return sitesMsg{i18n.T("sites.removed", d), list, err}
	}
}

func (m Model) appsUpdate(msg tea.Msg) (Model, tea.Cmd, bool) {
	switch msg := msg.(type) {
	case appsToggledMsg:
		if msg.err != nil {
			m.note = errText(msg.err)
		}
		return m, m.refresh(), true
	case excludedMsg:
		if msg.err != nil {
			m.note = errText(msg.err)
			return m, nil, true
		}
		m.apps.excluded = msg.list
		m.note = i18n.T("exclude.added", msg.domain)
		return m, nil, true
	case sitesMsg:
		if msg.err != nil {
			m.note = errText(msg.err)
			return m, nil, true
		}
		a := &m.apps
		a.sites = msg.list
		a.cursor = max(min(a.cursor, a.rows()-1), 0)
		m.note = msg.note
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
		a := &m.apps
		a.sites, a.skipped = msg.list, msg.skipped
		a.cursor = max(min(a.cursor, a.rows()-1), 0)
		note := []string{i18n.N("sites_import_tui.added", msg.added)}
		if n := len(msg.skipped); n > 0 {
			note = append(note, i18n.N("sites_import_tui.skipped", n))
		}
		if msg.added > 0 {
			note = append(note, i18n.T("sites_import_tui.restart"))
		}
		m.note = strings.Join(note, " ")
		return m, nil, true
	}
	return m, nil, false
}

// appsKey feeds the text input while typing and moves the cursor over apps and sites.
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

// appsView lists apps, the user's sites and the exclusions in one list,
// each with what it is; the bypass applies to the marked ones. Under the
// list: the selected row in full when it is cut, what to do when its probe
// failed, and the lines the last import skipped.
func (m Model) appsView(w, h int) string {
	a := m.apps
	type entry struct {
		on         bool
		name, kind string
		fail       probe.Category
		note       string
	}
	var entries []entry
	for _, app := range a.apps {
		entries = append(entries, entry{m.appEnabled(app.ID), app.Name, i18n.T("shell.sites.app"), probe.Overall(m.probe[app.ID]), ops.AppNote(app)})
	}
	for _, d := range a.sites {
		entries = append(entries, entry{on: true, name: d, kind: i18n.T("shell.sites.yours"), fail: probe.Overall(m.probe[d])})
	}
	for _, d := range a.excluded {
		entries = append(entries, entry{name: d, kind: i18n.T("shell.sites.excluded")})
	}
	describe := func(e entry) string {
		s := mutedStyle.Render(e.kind)
		switch {
		case e.fail == probe.NoAddress:
			s += mutedStyle.Render(", " + e.fail.What())
		case e.fail.Failed():
			s += mutedStyle.Render(", ") + badStyle.Render(e.fail.What())
		}
		if e.note != "" {
			s += mutedStyle.Render(", " + e.note)
		}
		return s
	}
	nameW := 0
	for _, e := range entries {
		nameW = max(nameW, lipgloss.Width(e.name))
	}
	nameW = min(nameW, w/2)
	// A long description wraps under itself, so no note or failure is cut.
	var lines []string
	first := make([]int, len(entries)) // each entry's first line
	for i, e := range entries {
		name := lipgloss.NewStyle().Width(nameW).MaxWidth(nameW)
		if e.fail == probe.NoAddress {
			name = name.Foreground(muted)
		}
		head := m.listMark(i == a.cursor && !a.typing) + m.mark(e.on) + " " + name.Render(e.name) + "  "
		indent := lipgloss.Width(head)
		what := describe(e)
		first[i] = len(lines)
		for j, l := range strings.Split(lipgloss.NewStyle().Width(max(w-indent, 10)).Render(what), "\n") {
			if j == 0 {
				lines = append(lines, head+l)
			} else {
				lines = append(lines, strings.Repeat(" ", indent)+l)
			}
		}
	}
	cursorLine := 0
	if a.cursor < len(first) {
		cursorLine = first[a.cursor]
	}
	var tail []string
	if a.typing {
		q := i18n.T("tui.apps_screen.which_site")
		switch a.ask {
		case askSite:
			q = i18n.T("tui.apps_screen.which_add")
		case askFile:
			q = i18n.T("sites_import_tui.which")
		}
		tail = append(strings.Split(lipgloss.NewStyle().Width(w).Render(q), "\n"), a.input.View())
		tail = append([]string{""}, tail...)
	} else {
		if i := a.cursor; i < len(entries) && entries[i].fail != probe.None {
			tail = append(tail, "")
			tail = append(tail, strings.Split(mutedStyle.Width(w).Render(probeTodo(entries[i].fail)), "\n")...)
		}
		if sk := a.skippedView(w, max(h/3, 3)); len(sk) > 0 {
			tail = append(append(tail, ""), sk...)
		}
	}
	lines = scroll(lines, cursorLine, h-len(tail), w)
	return strings.Join(append(lines, tail...), "\n")
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
