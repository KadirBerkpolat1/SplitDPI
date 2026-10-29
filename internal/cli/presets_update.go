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
	url := presets.UpdateURL
	fmt.Fprintln(w, i18n.T("presets.update.fetching", url))
	set, err := presets.Update(ctx, o.HTTP, o.P, url)
	if err != nil {
		return err
	}
	fmt.Fprintln(w, i18n.T("presets.update.done", set.Version, len(set.Apps)))
	for _, wn := range set.Warnings {
		fmt.Fprintln(w, "  "+wn)
	}
	return nil
}
