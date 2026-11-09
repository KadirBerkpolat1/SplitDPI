package cli

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/KadirBerkpolat1/SplitDPI/internal/i18n"
	"github.com/KadirBerkpolat1/SplitDPI/internal/ops"
	"github.com/KadirBerkpolat1/SplitDPI/internal/presets"
)

func runPresetsUpdate(ctx context.Context, o *ops.Ops, w io.Writer, args []string) error {
	if len(args) != 0 {
		return usageError(i18n.T("presets.update.usage"))
	}
	fmt.Fprintln(w, i18n.T("presets.update.fetching", presets.UpdateURL))
	version, apps, err := o.PresetsUpdate(ctx)
	if err != nil {
		return presetsUpdateError(err)
	}
	fmt.Fprintln(w, i18n.T("presets.update.done", version, apps))
	for _, wn := range o.PresetsList().Warnings {
		fmt.Fprintln(w, "  "+wn)
	}
	return nil
}

// presetsUpdateError words a checksum that failed the presets release: why
// nothing was changed and what to do. Other errors stay as they are.
func presetsUpdateError(err error) error {
	switch {
	case errors.Is(err, presets.ErrNoChecksum):
		return errors.New(i18n.T("presets.update.no_checksum"))
	case errors.Is(err, presets.ErrBadChecksum):
		return errors.New(i18n.T("presets.update.bad_checksum"))
	case errors.Is(err, presets.ErrChecksumMismatch):
		return errors.New(i18n.T("presets.update.mismatch"))
	}
	return err
}
