// Package i18n looks up user-facing strings in embedded TOML locales.
package i18n

import (
	"embed"
	"fmt"
	"maps"
	"strings"

	"github.com/BurntSushi/toml"
)

//go:embed locales/*.toml
var locales embed.FS

var supported = []string{"en", "tr", "ru"}

var messages map[string]string

// Detect picks the UI language: flag, then LC_ALL, LC_MESSAGES, LANG, then en.
// The first non-empty value decides; an unsupported language yields en.
func Detect(flag string, getenv func(string) string) string {
	v := flag
	for _, k := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		if v != "" {
			break
		}
		v = getenv(k)
	}
	v = strings.ToLower(v)
	if i := strings.IndexAny(v, "_.@"); i >= 0 {
		v = v[:i]
	}
	for _, s := range supported {
		if v == s {
			return s
		}
	}
	return "en"
}

// Init loads lang over the English source, so untranslated keys show English.
func Init(lang string) {
	messages = map[string]string{}
	for _, l := range []string{"en", lang} {
		m, err := parse(l)
		if err != nil {
			panic(fmt.Sprintf("i18n: locale %s: %v", l, err))
		}
		maps.Copy(messages, m)
	}
}

// parse returns one embedded locale as flat dotted keys; a missing locale is empty.
func parse(lang string) (map[string]string, error) {
	out := map[string]string{}
	b, err := locales.ReadFile("locales/" + lang + ".toml")
	if err != nil {
		return out, nil
	}
	var m map[string]any
	if err := toml.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	flatten(out, "", m)
	return out, nil
}

func flatten(out map[string]string, prefix string, m map[string]any) {
	for k, v := range m {
		switch v := v.(type) {
		case string:
			out[prefix+k] = v
		case map[string]any:
			flatten(out, prefix+k+".", v)
		}
	}
}

// Enum shows a stored value (a state, a mode) as the message group.value,
// or the value itself when there is no such message.
func Enum(group, value string) string {
	if msg, ok := messages[group+"."+value]; ok {
		return msg
	}
	return value
}

// T returns the message for key, formatted with args. A missing key returns "!key!".
func T(key string, args ...any) string {
	msg, ok := messages[key]
	if !ok {
		return "!" + key + "!"
	}
	if len(args) == 0 {
		return msg
	}
	return fmt.Sprintf(msg, args...)
}
