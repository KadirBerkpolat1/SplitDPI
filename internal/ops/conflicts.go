package ops

import (
	"context"

	"github.com/KadirBerkpolat1/SplitDPI/internal/doctor"
)

// ConflictError means another DPI-bypass tool is running; Checks are its Blockers.
type ConflictError struct{ Checks []doctor.Check }

func (e ConflictError) Error() string { return e.Checks[0].Msg }

// refuseConflicts returns a ConflictError if doctor finds a conflicting
// Blocker. `run` and `scan` both refuse: another bypass skews everything.
func (o *Ops) refuseConflicts(ctx context.Context) error {
	var bad []doctor.Check
	for _, c := range doctor.Conflicts(ctx, o.doctorEnv()) {
		if c.Severity == doctor.Blocker {
			bad = append(bad, c)
		}
	}
	if len(bad) > 0 {
		return ConflictError{Checks: bad}
	}
	return nil
}
