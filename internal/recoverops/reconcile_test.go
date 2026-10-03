package recoverops

import (
	"io"
	"log/slog"
	"testing"

	"k8s.io/client-go/kubernetes/fake"
)

func quietLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// Reconciler executes exactly one PROPOSED incident and leaves others alone.
func TestReconcileOnceExecutesProposed(t *testing.T) {
	s := testStore(t)
	pol := Policy{Namespace: WantNamespace, Deployment: WantDeployment}
	dep := fakeDeployment("uid-r", "rv-1", "good:1", 2)
	cs := fake.NewSimpleClientset(dep)
	lp, err := NewLivePatcher(cs, WantNamespace)
	if err != nil {
		t.Fatal(err)
	}
	raw, _, err := TemplateHash(dep.Spec.Template)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.RegisterGood(TargetUID(pol), raw); err != nil {
		t.Fatal(err)
	}
	in, _, err := s.CreateIncident("occ-rec-1", TargetUID(pol), "polhash", "{}")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Transition(in.ID, StObserved, "{}"); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordProposal(in.ID, EvalOutcome{Eligible: true, Reason: "eligible"}, ActProposed); err != nil {
		t.Fatal(err)
	}
	exec, _ := ReconcileOnce(s, lp, pol, quietLog())
	if exec != 1 {
		t.Fatalf("want 1 executed, got %d", exec)
	}
	cur, _ := s.GetIncident(in.ID)
	if cur.State != StVerifying {
		t.Fatalf("state %s, want VERIFYING", cur.State)
	}
	// Second tick: nothing left to execute (no loop).
	exec, _ = ReconcileOnce(s, lp, pol, quietLog())
	if exec != 0 {
		t.Fatalf("want 0 on re-tick, got %d", exec)
	}
}
