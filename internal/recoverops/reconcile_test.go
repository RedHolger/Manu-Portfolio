package recoverops

import (
	"io"
	"log/slog"
	"testing"
)

func quietLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// Reconciler executes exactly one PROPOSED incident and leaves others alone.
func TestReconcileOnceExecutesProposed(t *testing.T) {
	s, lp, pol, id := safetyFixture(t)
	exec, _ := ReconcileOnce(s, lp, pol, quietLog())
	if exec != 1 {
		t.Fatalf("want 1 executed, got %d", exec)
	}
	cur, _ := s.GetIncident(id)
	if cur.State != StVerifying {
		t.Fatalf("state %s, want VERIFYING", cur.State)
	}
	// Second tick: nothing left to execute (no loop).
	exec, _ = ReconcileOnce(s, lp, pol, quietLog())
	if exec != 0 {
		t.Fatalf("want 0 on re-tick, got %d", exec)
	}
}
