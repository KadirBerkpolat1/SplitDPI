package tui

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/KadirBerkpolat1/SplitDPI/internal/doctor"
	"github.com/KadirBerkpolat1/SplitDPI/internal/i18n"
	"github.com/KadirBerkpolat1/SplitDPI/internal/ops"
	"github.com/KadirBerkpolat1/SplitDPI/internal/sysinfo"
)

type pkgsMsg struct{ err error }

// offerPkgs reports whether the checks stop to offer installing the missing
// packages: the package manager is known and the user has not chosen to go
// on without them in this setup.
func (w wizard) offerPkgs() bool {
	return len(w.pkgs.Tools) > 0 && w.pkgs.CanInstall() && !w.pkgsSkipped
}

// offersTool reports whether the offered packages install the tool a check
// with this ID is about.
func (w wizard) offersTool(id string) bool {
	return slices.ContainsFunc(w.pkgs.Tools, func(t sysinfo.Tool) bool { return t.ID == id })
}

// canSkipPkgs reports whether the setup can go on without the offered
// packages: none of them is what blocks, and the flow is not a site scan,
// which always runs blockcheck2.
func (w wizard) canSkipPkgs() bool {
	return !w.siteFlow && !slices.ContainsFunc(blockers(w.checks), func(c doctor.Check) bool { return w.offersTool(c.ID) })
}

// pkgsActions installs the packages or, when the setup can, goes on without them.
func (m Model) pkgsActions(again action) []action {
	acts := []action{{Key: "enter", Label: i18n.T("tui.pkgs.install"), CLI: ops.InstallHint(m.wiz.pkgs), Do: (*Model).confirmPkgs}}
	if m.wiz.canSkipPkgs() {
		acts = append(acts, action{Key: "c", Label: i18n.T("tui.pkgs.skip"), Do: (*Model).skipPkgs})
	}
	again.Key = "r"
	return append(acts, again)
}

// confirmPkgs asks once before the package manager installs them.
func (m *Model) confirmPkgs() tea.Cmd {
	p := m.wiz.pkgs
	m.dialog = &confirm{
		title:    i18n.T("tui.pkgs.title"),
		changes:  []string{i18n.T("tui.pkgs.change", strings.Join(p.Names, ", "), p.Manager), i18n.T("deps.runs", p.Command())},
		undoHead: i18n.T("tui.pkgs.undo"),
		undo:     p.RemoveHint(),
		yes:      (*Model).installPkgs,
	}
	return nil
}

// installPkgs installs the missing packages, then the checks rerun.
func (m *Model) installPkgs() tea.Cmd {
	w := &m.wiz
	if w.pkgsInstalling {
		return nil
	}
	w.pkgsInstalling, w.pkgsErr, m.note = true, nil, ""
	b, ctx, logs, p := m.b, m.ctx, m.logs, w.pkgs
	return func() tea.Msg {
		fmt.Fprintln(logs, i18n.T("deps.installing", strings.Join(p.Names, ", "), p.Manager))
		return pkgsMsg{b.InstallPackages(ctx, p)}
	}
}

// skipPkgs goes on without the packages; this setup does not offer them again.
func (m *Model) skipPkgs() tea.Cmd {
	m.wiz.pkgsSkipped = true
	m.checked()
	return nil
}

func (m Model) pkgsDone(msg pkgsMsg) (Model, tea.Cmd) {
	w := &m.wiz
	w.pkgsInstalling = false
	if msg.err != nil {
		w.pkgsErr = msg.err
		fmt.Fprintln(m.logs, msg.err)
		return m, nil
	}
	w.installed = i18n.T("deps.installed", strings.Join(w.pkgs.Names, ", "))
	cmd := m.runDoctor()
	return m, cmd
}

// pkgsView sums up the offered packages and how their install goes. A failed
// install shows the first line of the package manager's error; all of it is
// in the log.
func (w wizard) pkgsView() []string {
	p := w.pkgs
	lines := []string{"", i18n.T("tui.pkgs.missing", strings.Join(p.Names, ", "))}
	switch {
	case w.pkgsInstalling:
		lines = append(lines, i18n.T("deps.installing", strings.Join(p.Names, ", "), p.Manager))
	case w.pkgsErr != nil:
		err, hint := w.pkgsErr, p.Hint()
		var ie ops.InstallError
		if errors.As(err, &ie) {
			err, hint = ie.Err, ie.Hint
		}
		first, _, _ := strings.Cut(err.Error(), "\n")
		lines = append(lines, badStyle.Render(i18n.T("tui.pkgs.failed", first)), i18n.T("deps.hint", commandStyle.Render(hint)))
	}
	return lines
}
