package tui

import (
	"context"
	"fmt"
	"net/netip"
	"slices"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/KadirBerkpolat1/SplitDPI/internal/dns"
	"github.com/KadirBerkpolat1/SplitDPI/internal/i18n"
	"github.com/KadirBerkpolat1/SplitDPI/internal/ops"
	"github.com/KadirBerkpolat1/SplitDPI/internal/probe"
)

// dnsQuestion is the scan asking, from its own goroutine, whether to turn on
// encrypted DNS; answer gets the provider id, or "" to keep the system's.
type dnsQuestion struct {
	poisoned []probe.DNSResult
	answer   chan string
}

// askDNS is the scan's DNS check: it hands the question to the TUI and waits
// for the answer, or for the scan to end.
func askDNS(ctx context.Context, asks chan<- dnsQuestion, poisoned []probe.DNSResult) string {
	q := dnsQuestion{poisoned: poisoned, answer: make(chan string, 1)}
	select {
	case asks <- q:
	case <-ctx.Done():
		return ""
	}
	select {
	case id := <-q.answer:
		return id
	case <-ctx.Done():
		return ""
	}
}

// dnsAsk is the dialog showing a dnsQuestion: yes (Cloudflare), another
// provider, or no.
type dnsAsk struct {
	q         dnsQuestion
	cursor    int
	pick      bool // the provider list is shown
	providers []ops.DNSProvider
}

const (
	askYes = iota
	askOther
	askNo
)

const (
	recommendedDNS = "cloudflare"
	undoDNS        = "sudo splitdpi dns off"
)

func (m *Model) openDNSAsk(q dnsQuestion) {
	var ps []ops.DNSProvider
	for _, p := range m.b.DNSProviders() {
		if p.ID != dns.Off {
			ps = append(ps, p)
		}
	}
	m.ask = &dnsAsk{q: q, providers: ps}
	for _, r := range q.poisoned {
		fmt.Fprintln(m.logs, i18n.T("dns2.scan_poisoned", r.Domain, joinAddrs(r.System), joinAddrs(r.DoH)))
	}
}

func joinAddrs(addrs []netip.Addr) string {
	s := make([]string, len(addrs))
	for i, a := range addrs {
		s[i] = a.String()
	}
	return strings.Join(s, ", ")
}

// answerDNS closes the dialog and lets the scan go on with id.
func (m *Model) answerDNS(id string) {
	a := m.ask
	m.ask = nil
	a.q.answer <- id
	line := i18n.T("dns2.scan_kept")
	if id != "" {
		name := id
		if i := slices.IndexFunc(a.providers, func(p ops.DNSProvider) bool { return p.ID == id }); i >= 0 {
			name = a.providers[i].Name
		}
		line = i18n.T("dns2.scan_on", name)
		m.wiz.dnsOn = true
	}
	if m.wiz.progress != nil {
		fmt.Fprintln(m.wiz.progress, line)
	}
}

func (m Model) dnsAskKey(s string) (tea.Model, tea.Cmd) {
	a := *m.ask
	rows := 3
	if a.pick {
		rows = len(a.providers)
	}
	switch s {
	case "up", "k":
		a.cursor = (a.cursor + rows - 1) % rows
	case "down", "j":
		a.cursor = (a.cursor + 1) % rows
	case "y", i18n.T("confirm_keys.yes"):
		if !a.pick {
			m.answerDNS(recommendedDNS)
			return m, nil
		}
	case "n", i18n.T("confirm_keys.no"):
		if !a.pick {
			m.answerDNS("")
			return m, nil
		}
	case "esc":
		if !a.pick {
			m.answerDNS("")
			return m, nil
		}
		a.pick, a.cursor = false, askOther
	case "enter":
		switch {
		case a.pick:
			m.answerDNS(a.providers[a.cursor].ID)
			return m, nil
		case a.cursor == askYes:
			m.answerDNS(recommendedDNS)
			return m, nil
		case a.cursor == askNo:
			m.answerDNS("")
			return m, nil
		}
		a.pick, a.cursor = true, 0
	}
	m.ask = &a
	return m, nil
}

// maxPoisonedShown keeps the question short; the log has every domain.
const maxPoisonedShown = 3

// dnsAskBox is the question, or the provider list, with its rows clickable.
func (m Model) dnsAskBox() popupBox {
	a := m.ask
	p := popupBox{head: 1, tail: 1}
	p.lines = []string{titleStyle.Render(i18n.T("scan_page.dns.title")), ""}
	back := i18n.T("tui.act.cancel")
	if a.pick {
		p.add(m, i18n.T("scan_page.dns.pick"))
		for i, pr := range a.providers {
			p.choice(m.cursorMark(i == a.cursor)+pr.Name, "#"+strconv.Itoa(i))
		}
		p.lines = append(p.lines, "")
		p.add(m, mutedStyle.Render(a.providers[a.cursor].Note))
		back = i18n.T("tui.act.back")
	} else {
		p.add(m, i18n.T("scan_page.dns.poisoned"))
		for i, r := range a.q.poisoned {
			if i == maxPoisonedShown {
				p.lines = append(p.lines, "   "+mutedStyle.Render(i18n.N("scan_page.dns.more", len(a.q.poisoned)-i)))
				break
			}
			p.lines = append(p.lines, " "+m.g.bullet+" "+r.Domain)
		}
		p.add(m, i18n.T("scan_page.dns.why"))
		p.lines = append(p.lines, "")
		for i, k := range []string{"scan_page.dns.yes", "scan_page.dns.other", "scan_page.dns.no"} {
			p.choice(m.cursorMark(i == a.cursor)+i18n.T(k), "#"+strconv.Itoa(i))
		}
	}
	p.lines = append(p.lines, "")
	p.add(m, i18n.T("shell.confirm.undo"))
	p.lines = append(p.lines, "   "+commandStyle.Render(undoDNS), "")
	p.keys([2]string{"enter", i18n.T("scan_page.dns.ok")}, [2]string{"esc", back})
	return p
}
