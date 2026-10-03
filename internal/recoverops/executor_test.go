package recoverops

import (
	"errors"
	"testing"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func seedIncident(t *testing.T, s *Store) string {
	t.Helper()
	in, _, err := s.CreateIncident("occ-1", "sre-lab/api-stable", "polhash", "{}")
	if err != nil {
		t.Fatal(err)
	}
	return in.ID
}

// Accepted-but-response-lost must re-read, not blindly re-patch: desired
// present → verify, old present → one bounded retry.
func TestResponseLostDesiredPresentVerifies(t *testing.T) {
	live := TargetSnapshot{UID: "uid-1", ResourceVersion: "rv-2", TemplateHash: "desired"}
	if got := DecideOnResponseLost(live, "uid-1", "before", "desired"); got != DecVerify {
		t.Fatalf("got %s, want verify", got)
	}
}

func TestResponseLostOldPresentRetries(t *testing.T) {
	live := TargetSnapshot{UID: "uid-1", ResourceVersion: "rv-1", TemplateHash: "before"}
	if got := DecideOnResponseLost(live, "uid-1", "before", "desired"); got != DecRetry {
		t.Fatalf("got %s, want retry", got)
	}
}

// Target replacement (new UID) escalates, never overwrites.
func TestTargetReplacementEscalates(t *testing.T) {
	live := TargetSnapshot{UID: "uid-2", ResourceVersion: "rv-9", TemplateHash: "desired"}
	if got := DecideAfterRestart(live, "uid-1", "before", "desired"); got != DecEscalat {
		t.Fatalf("got %s, want escalate", got)
	}
	if got := DecideOnPatchError(ErrConflictUID, live, "uid-1", "before", "desired"); got != DecEscalat {
		t.Fatalf("got %s, want escalate", got)
	}
}

// Operator conflict (live template is neither before nor desired) escalates.
func TestOperatorConflictEscalates(t *testing.T) {
	live := TargetSnapshot{UID: "uid-1", ResourceVersion: "rv-3", TemplateHash: "third-party-edit"}
	if got := DecideOnPatchError(ErrConflictTemplate, live, "uid-1", "before", "desired"); got != DecEscalat {
		t.Fatalf("got %s, want escalate", got)
	}
	if got := DecideAfterRestart(live, "uid-1", "before", "desired"); got != DecEscalat {
		t.Fatalf("got %s, want escalate", got)
	}
}

// API timeout and status-only RV conflict permit a bounded retry.
func TestTimeoutAndRVConflictRetry(t *testing.T) {
	live := TargetSnapshot{UID: "uid-1", ResourceVersion: "rv-4", TemplateHash: "before"}
	if got := DecideOnPatchError(ErrTimeout, live, "uid-1", "before", "desired"); got != DecRetry {
		t.Fatalf("timeout: got %s, want retry", got)
	}
	if got := DecideOnPatchError(ErrConflictRV, live, "uid-1", "before", "desired"); got != DecRetry {
		t.Fatalf("rv-conflict: got %s, want retry", got)
	}
}

// Restart after intent (claimed, nothing applied): old template present →
// exactly one bounded retry path, and claim is idempotent (no 2nd action).
func TestRestartAfterIntentSingleLogicalAction(t *testing.T) {
	s := testStore(t)
	id := seedIncident(t, s)
	if err := s.ClaimForExecution(id, "uid-1", "rv-1", "before", "desired"); err != nil {
		t.Fatal(err)
	}
	if err := s.ClaimForExecution(id, "uid-1", "rv-1", "before", "desired"); err != nil {
		t.Fatal(err)
	}
	acts, err := s.ActionsFor(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(acts) != 1 {
		t.Fatalf("want exactly 1 logical action, got %d", len(acts))
	}
	live := TargetSnapshot{UID: "uid-1", ResourceVersion: "rv-1", TemplateHash: "before"}
	if got := DecideAfterRestart(live, "uid-1", "before", "desired"); got != DecRetry {
		t.Fatalf("got %s, want retry", got)
	}
}

// Restart after application (desired present) → verify, not re-patch.
func TestRestartAfterApplicationVerifies(t *testing.T) {
	s := testStore(t)
	id := seedIncident(t, s)
	if err := s.ClaimForExecution(id, "uid-1", "rv-1", "before", "desired"); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkActionStatus(id, StVerifying, ""); err != nil {
		t.Fatal(err)
	}
	live := TargetSnapshot{UID: "uid-1", ResourceVersion: "rv-2", TemplateHash: "desired"}
	if got := DecideAfterRestart(live, "uid-1", "before", "desired"); got != DecVerify {
		t.Fatalf("got %s, want verify", got)
	}
}

// Unknown errors escalate rather than looping.
func TestUnknownErrorEscalates(t *testing.T) {
	live := TargetSnapshot{UID: "uid-1", ResourceVersion: "rv-1", TemplateHash: "before"}
	if got := DecideOnPatchError(errors.New("boom"), live, "uid-1", "before", "desired"); got != DecEscalat {
		t.Fatalf("got %s, want escalate", got)
	}
}
