package tui

import tea "github.com/charmbracelet/bubbletea"

// page is one tab and what the frame shows for it. Its state lives in Model;
// a page only reads and updates it.
type page interface {
	Title() string // the tab
	Hint() string  // what the page is for, on the frame's top border
	// Open runs when the page is selected; Close when another one is.
	Open(m *Model) tea.Cmd
	Close(m *Model)
	// Actions are the page's keys, the most used first: the hint bar shows
	// the first ones that fit, the help all of them.
	Actions(m Model) []action
	// Key sees every key first; false means not handled. A page that types
	// into an input takes every key, so that q and the digits can be typed.
	Key(m Model, k tea.KeyMsg, s string) (Model, tea.Cmd, bool)
	// Update sees every message on any page; false means not the page's.
	Update(m Model, msg tea.Msg) (Model, tea.Cmd, bool)
	// View draws the page body in width x height cells.
	View(m Model, width, height int) string
}

// The tabs, in order; 1 to 5 select them.
var pages = []page{statusPage{}, sitesPage{}, setupPage{}, logsPage{}, settingsPage{}}

const (
	pgStatus = iota
	pgSites
	pgSetup
	pgLogs
	pgSettings
)

// action is one thing the user can do on a page. CLI is the command that
// does the same outside the TUI; the help lists it.
type action struct {
	Key, Label, CLI string
	Do              func(m *Model) tea.Cmd
}

// noClose is Close for the pages that keep nothing running.
type noClose struct{}

func (noClose) Close(*Model) {}
