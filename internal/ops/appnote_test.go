package ops

import (
	"testing"

	"github.com/KadirBerkpolat1/SplitDPI/internal/i18n"
	"github.com/KadirBerkpolat1/SplitDPI/internal/presets"
)

func TestAppNote(t *testing.T) {
	defer i18n.Init("en")
	i18n.Init("tr")
	tg := presets.App{ID: "telegram", Note: "MTProto to raw IPs is not covered"}
	if got := AppNote(tg); got == tg.Note || got == "" {
		t.Errorf("telegram in tr = %q, want the translation", got)
	}
	other := presets.App{ID: "newapp", Note: "data note"}
	if got := AppNote(other); got != "data note" {
		t.Errorf("untranslated = %q, want the data note", got)
	}
	if got := AppNote(presets.App{ID: "telegram"}); got != "" {
		t.Errorf("no data note = %q, want none", got)
	}
}

// Every embedded app with a note has it in each UI language.
func TestEmbeddedAppNotesTranslated(t *testing.T) {
	defer i18n.Init("en")
	set := presets.Load(presets.Embedded())
	for _, lang := range i18n.Supported() {
		i18n.Init(lang)
		for id, a := range set.Apps {
			if a.Note == "" {
				continue
			}
			if i18n.Enum("appnote", id) == id || (lang != "en" && AppNote(a) == a.Note) {
				t.Errorf("%s: no appnote.%s translation", lang, id)
			}
		}
	}
}
