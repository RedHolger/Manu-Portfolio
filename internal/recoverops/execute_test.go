package recoverops

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestExecuteOnceRestoresTemplate(t *testing.T) {
	s := testStore(t)
	pol := Policy{Namespace: WantNamespace, Deployment: WantDeployment}
	dep := fakeDeployment("uid-9", "rv-1", "good:1", 2)
	cs := fake.NewSimpleClientset(dep)
	lp, err := NewLivePatcher(cs, WantNamespace)
	if err != nil {
		t.Fatal(err)
	}
	snap, _, err := lp.Get(WantDeployment)
	if err != nil {
		t.Fatal(err)
	}
	goodRaw, _, err := TemplateHash(dep.Spec.Template)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.RegisterGood(TargetUID(pol), goodRaw); err != nil {
		t.Fatal(err)
	}
	in, _, err := s.CreateIncident("occ-exec-1", TargetUID(pol), "polhash", "{}")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Transition(in.ID, StObserved, "{}"); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordProposal(in.ID, EvalOutcome{Eligible: true, Reason: "eligible", SnapshotHash: snap.TemplateHash}, ActProposed); err != nil {
		t.Fatal(err)
	}
	// Simulate bad config drift on the cluster.
	bad := dep.DeepCopy()
	bad.Spec.Template.Spec.Containers[0].Image = "bad:lab-test"
	badRaw, _, _ := TemplateHash(bad.Spec.Template)
	_ = badRaw
	cs.AppsV1().Deployments(WantNamespace).Update(t.Context(), bad, metav1.UpdateOptions{})
	out, err := ExecuteOnce(s, lp, pol, in.ID)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if out.To != StVerifying {
		t.Fatalf("want VERIFYING, got %s (%s)", out.To, out.Reason)
	}
	cur, _ := s.GetIncident(in.ID)
	if cur.State != StVerifying {
		t.Fatalf("incident state %s, want VERIFYING", cur.State)
	}
}
