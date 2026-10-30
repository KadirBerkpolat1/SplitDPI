package cli

import (
	"context"
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
		return err
	}
	fmt.Fprintln(w, i18n.T("presets.update.done", version, apps))
	for _, wn := range o.PresetsList().Warnings {
		fmt.Fprintln(w, "  "+wn)
	}
	return nil
}
