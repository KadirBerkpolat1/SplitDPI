package cli

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/KadirBerkpolat1/SplitDPI/internal/i18n"
	"github.com/KadirBerkpolat1/SplitDPI/internal/ops"
)

func init() {
	commands["strategy"] = Command{Name: "strategy", Root: true, Help: "cmd.strategy.help", Run: runStrategy}
}

func runStrategy(_ context.Context, o *ops.Ops, w io.Writer, args []string) error {
	switch {
	case len(args) == 1 && args[0] == "list":
		list, cur, err := o.StrategyList()
		if err != nil {
			return err
		}
		width := 0
		for _, ch := range list {
			width = max(width, len(ch.Strategy.ID))
		}
		found := false
		for _, ch := range list {
			mark := " "
			if cur != nil && cur.ID == ch.Strategy.ID {
				mark, found = "*", true
			}
			fmt.Fprintf(w, "%s %s%s  %s\n", mark, ch.Strategy.ID, strings.Repeat(" ", width-len(ch.Strategy.ID)), ch.From())
		}
		if cur != nil && !found {
			fmt.Fprintln(w, i18n.T("strategy.current", cur.ID, cur.Source))
		}
		return nil
	case len(args) == 2 && args[0] == "set":
		ch, running, err := o.StrategySet(args[1])
		if err != nil {
			return err
		}
		fmt.Fprintln(w, i18n.T("strategy.set", ch.Strategy.ID, ch.From()))
		if running {
			fmt.Fprintln(w, i18n.T("strategy.restart"))
		}
		return nil
	}
	return usageError(i18n.T("strategy.usage"))
}
