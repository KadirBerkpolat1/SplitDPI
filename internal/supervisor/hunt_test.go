package supervisor_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/KadirBerkpolat1/SplitDPI/internal/supervisor"
)

// While nfqws2 waits to be restarted, the status must not say running, or a
// crash loop passes for a working start.
func TestBackoffStatusIsRestarting(t *testing.T) {
	p := setup(t)
	h := newHarness(t, p, true)
	var during []supervisor.Status
	h.s.Sleep = func(time.Duration) {
		st, err := supervisor.ReadStatus(p)
		if err != nil {
			t.Error(err)
		}
		during = append(during, st)
	}
	if err := h.s.Run(context.Background(), out, rules, meta()); !errors.Is(err, supervisor.ErrCrashLoop) {
		t.Fatalf("got %v, want ErrCrashLoop", err)
	}
	if len(during) != 4 {
		t.Fatalf("%d backoffs, want 4", len(during))
	}
	for i, st := range during {
		if st.State != "restarting" || st.ChildPID != 0 || st.LastError == "" {
			t.Errorf("backoff %d: status %+v", i+1, st)
		}
	}
}
