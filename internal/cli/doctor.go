package cli

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/KadirBerkpolat1/SplitDPI/internal/doctor"
	"github.com/KadirBerkpolat1/SplitDPI/internal/i18n"
	"github.com/KadirBerkpolat1/SplitDPI/internal/ops"
)

func init() {
	commands["doctor"] = Command{Name: "doctor", Root: true, Help: "cmd.doctor.help", Run: runDoctor}
}

func runDoctor(ctx context.Context, o *ops.Ops, w io.Writer, args []string) error {
	if len(args) != 0 {
		return usageError(i18n.T("doctor.usage"))
	}
	cs := o.Doctor(ctx)
	printChecks(w, cs)
	if doctor.ExitCode(cs) != 0 {
		n := 0
		for _, c := range cs {
			if c.Severity == doctor.Blocker {
				n++
			}
		}
		return errors.New(i18n.N("doctor.blockers", n))
	}
	return nil
}

var marks = map[doctor.Severity]string{doctor.OK: "✓", doctor.Warn: "!", doctor.Blocker: "✗"}

func printChecks(w io.Writer, cs []doctor.Check) {
	for _, c := range cs {
		fmt.Fprintf(w, "%s %s: %s\n", marks[c.Severity], c.ID, c.Msg)
		if c.Fix != "" {
			fmt.Fprintln(w, "    "+i18n.T("doctor.fix", c.Fix))
		}
	}
}
