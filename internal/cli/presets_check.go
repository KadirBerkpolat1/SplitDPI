package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/KadirBerkpolat1/SplitDPI/internal/i18n"
	"github.com/KadirBerkpolat1/SplitDPI/internal/ops"
	"github.com/KadirBerkpolat1/SplitDPI/internal/presets"
)

// runPresetsCheck validates a preset tree the way `presets update` would load
// it. The presets repository's CI runs it on every change.
func runPresetsCheck(_ context.Context, _ *ops.Ops, w io.Writer, args []string) error {
	if len(args) != 1 {
		return usageError(i18n.T("presets.check.usage"))
	}
	if fi, err := os.Stat(args[0]); err != nil || !fi.IsDir() {
		return errors.New(i18n.T("presets.check.no_dir", args[0]))
	}
	s := presets.Load(os.DirFS(args[0]))
	for _, wn := range s.Warnings {
		fmt.Fprintln(w, wn)
	}
	switch {
	case len(s.Warnings) > 0:
		return errors.New(i18n.T("presets.check.bad", len(s.Warnings)))
	case len(s.Apps) == 0:
		return errors.New(i18n.T("presets.check.empty"))
	}
	fmt.Fprintln(w, i18n.T("presets.check.ok", s.Version, len(s.ISPs), len(s.Apps)))
	return nil
}
