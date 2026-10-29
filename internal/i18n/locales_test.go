package i18n

import (
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// verbs lists a message's format verbs by type, so translations may reorder
// arguments with explicit indexes (%[2]s) but not change them.
var verbRe = regexp.MustCompile(`%(\[\d+\])?[-+# 0-9.]*([a-zA-Z%])`)

func verbs(s string) []string {
	var out []string
	for _, m := range verbRe.FindAllStringSubmatch(s, -1) {
		if m[2] != "%" {
			out = append(out, m[2])
		}
	}
	slices.Sort(out)
	return out
}

func TestLocalesComplete(t *testing.T) {
	en, err := parse("en")
	if err != nil {
		t.Fatal(err)
	}
	for _, lang := range []string{"tr", "ru"} {
		got, err := parse(lang)
		if err != nil {
			t.Fatalf("%s: %v", lang, err)
		}
		for _, k := range slices.Sorted(maps.Keys(en)) {
			v, ok := got[k]
			if !ok {
				t.Errorf("%s: missing key %s", lang, k)
				continue
			}
			if !slices.Equal(verbs(v), verbs(en[k])) {
				t.Errorf("%s: %s has verbs %v, en has %v", lang, k, verbs(v), verbs(en[k]))
			}
		}
		for k := range got {
			if _, ok := en[k]; !ok {
				t.Errorf("%s: extra key %s not in en", lang, k)
			}
		}
	}
}

// Every user-facing literal in the CLI and the TUI goes through T. Format
// strings with only verbs and symbols are fine; so are these proper names.
var allowed = []string{"splitdpi", "dns", "[x]"}

var printRe = regexp.MustCompile(`(?:fmt\.Fprint(?:f|ln)?\(\w+, |Render\()"((?:[^"\\]|\\.)*)"`)

func TestNoHardcodedStrings(t *testing.T) {
	for _, dir := range []string{"../cli", "../tui"} {
		files, _ := filepath.Glob(filepath.Join(dir, "*.go"))
		for _, f := range files {
			if strings.HasSuffix(f, "_test.go") {
				continue
			}
			b, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			for _, m := range printRe.FindAllStringSubmatch(string(b), -1) {
				lit := verbRe.ReplaceAllString(m[1], "")
				lit = strings.ReplaceAll(lit, `\n`, "")
				for _, a := range allowed {
					lit = strings.ReplaceAll(lit, a, "")
				}
				if strings.ContainsFunc(lit, func(r rune) bool { return r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' }) {
					t.Errorf("%s: hardcoded text %q; use i18n.T", f, m[1])
				}
			}
		}
	}
}

func TestEnum(t *testing.T) {
	Init("tr")
	defer Init("en")
	if got := Enum("state", "running"); got == "running" || strings.HasPrefix(got, "!") {
		t.Errorf("state running in tr = %q", got)
	}
	if got := Enum("state", "weird"); got != "weird" {
		t.Errorf("unknown value = %q, want it unchanged", got)
	}
}
