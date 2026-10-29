package ops

import (
	"github.com/KadirBerkpolat1/SplitDPI/internal/i18n"
	"github.com/KadirBerkpolat1/SplitDPI/internal/presets"
)

// AppNote is a's note in the UI language (message appnote.<id>), else the
// preset data's own note. An app whose data has no note gets none.
func AppNote(a presets.App) string {
	if a.Note == "" {
		return ""
	}
	if msg := i18n.Enum("appnote", a.ID); msg != a.ID {
		return msg
	}
	return a.Note
}
