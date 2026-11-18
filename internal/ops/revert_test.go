package ops

import (
	"context"
	"os"
	"slices"
	"testing"
	"testing/fstest"

	"github.com/KadirBerkpolat1/SplitDPI/internal/supervisor"
)

// A service run that does not stop keeps its lock file, so that no second
// supervisor can start beside it; once it is gone, revert clears RunDir.
func TestRevertKeepsLockOfRunThatWontStop(t *testing.T) {
	shortStopWait(t, 0)
	ctx := context.Background()
	o, _ := takeoverOps(t, fstest.MapFS{"run/systemd/system": {Mode: os.ModeDir}}, nil)
	release := holdRunLock(t, o.P, "service", 4343)

	rep := o.Revert(ctx, false, false)
	if !slices.Contains(rep.Leftover, "splitdpi: "+ErrStopTimeout.Error()) || !slices.Contains(rep.Leftover, o.P.RunDir) {
		t.Errorf("leftover %q", rep.Leftover)
	}
	if slices.Contains(rep.Removed, o.P.RunDir) {
		t.Errorf("removed %q", rep.Removed)
	}
	if _, _, held := supervisor.LockInfo(o.P); !held {
		t.Fatal("lock file of the run still holding it was unlinked")
	}

	release()
	if rep := o.Revert(ctx, false, false); !slices.Contains(rep.Removed, o.P.RunDir) || len(rep.Leftover) != 0 {
		t.Errorf("after the run: %+v", rep)
	}
}
