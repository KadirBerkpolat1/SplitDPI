package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// glyphs are the drawing characters; the ASCII set is for terminals that
// cannot show box drawing or symbols.
type glyphs struct {
	border   lipgloss.Border
	logo     string
	dot      string
	cursor   string
	on, off  string // a toggle or a list entry the bypass applies to, or not
	fail     string // a failed check
	bullet   string
	sep      string
	menuKeys string
}

var (
	unicodeGlyphs = glyphs{
		border: lipgloss.RoundedBorder(),
		logo:   "◆", dot: "●", cursor: "▸", on: "✓", off: "✗", fail: "✗",
		bullet: "•", sep: "·", menuKeys: "↑↓",
	}
	asciiGlyphs = glyphs{
		border: lipgloss.Border{
			Top: "-", Bottom: "-", Left: "|", Right: "|",
			TopLeft: "+", TopRight: "+", BottomLeft: "+", BottomRight: "+",
		},
		logo: "*", dot: "*", cursor: ">", on: "[x]", off: "[ ]", fail: "FAIL",
		bullet: "-", sep: "-", menuKeys: "j/k",
	}
)

// glyphsFor picks ASCII on the Linux console and when the locale is not
// UTF-8; the first set of LC_ALL, LC_CTYPE and LANG is the locale.
func glyphsFor(getenv func(string) string) glyphs {
	if getenv("TERM") == "linux" {
		return asciiGlyphs
	}
	for _, k := range []string{"LC_ALL", "LC_CTYPE", "LANG"} {
		v := strings.ToLower(getenv(k))
		if v == "" {
			continue
		}
		if strings.Contains(v, "utf-8") || strings.Contains(v, "utf8") {
			return unicodeGlyphs
		}
		return asciiGlyphs
	}
	return asciiGlyphs
}
