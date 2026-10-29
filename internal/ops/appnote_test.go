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
