package tui

import tea "github.com/charmbracelet/bubbletea"

// page is one entry of the menu and what the right pane shows for it. Its
// state lives in Model; a page only reads and updates it.
type page interface {
	Title() string // the menu entry
	Hint() string  // what the page is for, next to its title
	// Open runs when the menu selects the page.
	Open(m *Model) tea.Cmd
	// Actions are the page's keys; the dock shows them as buttons.
	Actions(m Model) []action
	// Key sees keys first while the page has focus; false means not handled.
	Key(m Model, k tea.KeyMsg, s string) (Model, tea.Cmd, bool)
	// Update sees every message on any page; false means not the page's.
	Update(m Model, msg tea.Msg) (Model, tea.Cmd, bool)
	// View draws the page body in width x height cells.
	View(m Model, width, height int) string
}

// The menu, in order.
var pages = []page{statusPage{}, sitesPage{}, strategyPage{}, scanPage{}, logsPage{}, settingsPage{}}

const (
	pgStatus = iota
	pgSites
	pgStrategy
	pgScan
	pgLogs
	pgSettings
)

// action is one thing the user can do on a page. CLI is the command that
// does the same outside the TUI; the command line shows it for the focused
// action.
// An action without a key only carries the CLI command.
type action struct {
	Key, Label, CLI string
	Do              func(m *Model) tea.Cmd
}
