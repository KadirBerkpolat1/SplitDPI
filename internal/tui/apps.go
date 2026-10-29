package tui

import (
	"maps"
	"slices"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/KadirBerkpolat1/SplitDPI/internal/i18n"
	"github.com/KadirBerkpolat1/SplitDPI/internal/presets"
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
	addSite  bool // the input adds a site instead of an exclusion
}

type (
	appsToggledMsg struct{ err error }
	excludedMsg    struct {
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
	a.input.Placeholder = "example.com"
	a.input.CharLimit = 253
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
	case a.typing && a.addSite:
		return []action{{Key: "enter", Label: i18n.T("tui.apps_screen.add"), CLI: "sudo splitdpi sites add " + a.input.Value(), Do: (*Model).siteTyped}, cancel}
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
	startTyping := func(site bool) func(m *Model) tea.Cmd {
		return func(m *Model) tea.Cmd {
			m.apps.typing, m.apps.addSite, m.focus, m.inPage = true, site, 0, true
			m.apps.input.SetValue("")
			return m.apps.input.Focus()
		}
	}
	return []action{
		{Key: "a", Label: i18n.T("tui.act.add_site"), CLI: "sudo splitdpi sites add DOMAIN", Do: startTyping(true)},
		first,
		{Key: "e", Label: i18n.T("tui.apps_screen.site_broke"), CLI: "sudo splitdpi exclude add DOMAIN", Do: startTyping(false)},
	}
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
// each with what it is; the bypass applies to the marked ones.
func (m Model) appsView(w, h int) string {
	a := m.apps
	type entry struct {
		on         bool
		name, what string
	}
	var entries []entry
	for _, app := range a.apps {
		what := i18n.T("shell.sites.app")
		if app.Note != "" {
			what += ", " + app.Note
		}
		entries = append(entries, entry{m.appEnabled(app.ID), app.Name, what})
	}
	for _, d := range a.sites {
		entries = append(entries, entry{true, d, i18n.T("shell.sites.yours")})
	}
	for _, d := range a.excluded {
		entries = append(entries, entry{false, d, i18n.T("shell.sites.excluded")})
	}
	nameW := 0
	for _, e := range entries {
		nameW = max(nameW, lipgloss.Width(e.name))
	}
	nameW = min(nameW, w/2)
	lines := make([]string, len(entries))
	for i, e := range entries {
		name := lipgloss.NewStyle().Width(nameW).MaxWidth(nameW).Render(e.name)
		lines[i] = m.cursorMark(i == a.cursor && !a.typing) + m.mark(e.on) + " " + name + "  " + mutedStyle.Render(e.what)
	}
	var prompt []string
	if a.typing {
		q := i18n.T("tui.apps_screen.which_site")
		if a.addSite {
			q = i18n.T("tui.apps_screen.which_add")
		}
		prompt = append(strings.Split(lipgloss.NewStyle().Width(w).Render(q), "\n"), a.input.View())
		prompt = append([]string{""}, prompt...)
	}
	lines = scroll(lines, a.cursor, h-len(prompt), w)
	return strings.Join(append(lines, prompt...), "\n")
}
