package recoverops

import (
	"encoding/json"
	"fmt"
	"math"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	kt "k8s.io/client-go/testing"
)

func executionFixture(t *testing.T, s *Store, lp *LivePatcher, pol Policy, key string) string {
	t.Helper()
	dep, err := lp.Client.AppsV1().Deployments(WantNamespace).Get(t.Context(), WantDeployment, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	raw, hash, _ := TemplateHash(dep.Spec.Template)
	if _, err = s.RegisterBoundGood(TargetUID(pol), string(dep.UID), raw); err != nil {
		t.Fatal(err)
	}
	if err = s.SetMeta("mode", "enforce-lab"); err != nil {
		t.Fatal(err)
	}
	ev, _ := json.Marshal(map[string]any{"startsAt": time.Now().UTC().Format(time.RFC3339Nano), "labels": map[string]string{"namespace": WantNamespace, "deployment": WantDeployment}})
	in, _, err := s.CreateIncident(key, TargetUID(pol), pol.Hash, string(ev))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Transition(in.ID, StObserved, "{}"); err != nil {
		t.Fatal(err)
	}
	if err = s.RecordProposal(in.ID, EvalOutcome{Eligible: true, SnapshotHash: hash}, ActProposed); err != nil {
		t.Fatal(err)
	}
	dep.Spec.Template.Spec.Containers[0].Image = "bad:fixture"
	if _, err = lp.Client.AppsV1().Deployments(WantNamespace).Update(t.Context(), dep, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	return in.ID
}
func safetyFixture(t *testing.T) (*Store, *LivePatcher, Policy, string) {
	t.Helper()
	s := testStore(t)
	pol := testPolicy(t)
	cs := fake.NewSimpleClientset(fakeDeployment("uid-safe", "1", "good:1", 2))
	lp, _ := NewLivePatcher(cs, WantNamespace)
	id := executionFixture(t, s, lp, pol, "safety")
	return s, lp, pol, id
}
func TestExecutionRequiresModeProposalAndBinding(t *testing.T) {
	for _, kind := range []string{"mode", "proposal", "binding", "policy"} {
		t.Run(kind, func(t *testing.T) {
			s, p, pol, id := safetyFixture(t)
			switch kind {
			case "mode":
				s.SetMeta("mode", "observe")
			case "proposal":
				s.db.Exec(`DELETE FROM actions WHERE incident_id=?`, id)
			case "binding":
				s.db.Exec(`DELETE FROM known_good_bindings`)
			case "policy":
				pol.Hash = "different"
			}
			if _, err := ExecuteOnce(s, p, pol, id); err == nil {
				t.Fatal("unauthorized execution accepted")
			}
			for _, a := range p.Client.(*fake.Clientset).Actions() {
				if a.GetVerb() == "patch" {
					t.Fatal("unexpected mutation")
				}
			}
		})
	}
}
func TestLostResponseReconcilesWithoutSecondPatch(t *testing.T) {
	s, p, pol, id := safetyFixture(t)
	cs := p.Client.(*fake.Clientset)
	calls := 0
	cs.PrependReactor("patch", "deployments", func(a kt.Action) (bool, runtime.Object, error) {
		calls++
		dep, _ := cs.Tracker().Get(a.GetResource(), WantNamespace, WantDeployment)
		// Simulate durable patch followed by lost HTTP response.
		x, _ := s.Intent(id)
		var patch []map[string]json.RawMessage
		_ = json.Unmarshal(a.(kt.PatchAction).GetPatch(), &patch)
		raw := dep.DeepCopyObject()
		b, _ := json.Marshal(raw)
		var obj map[string]any
		json.Unmarshal(b, &obj)
		var template any
		json.Unmarshal([]byte(x.Template), &template)
		obj["spec"].(map[string]any)["template"] = template
		b, _ = json.Marshal(obj)
		json.Unmarshal(b, raw)
		cs.Tracker().Update(a.GetResource(), raw, WantNamespace)
		return true, nil, fmt.Errorf("connection lost after commit")
	})
	out, err := ExecuteOnce(s, p, pol, id)
	if err != nil || out.To != StVerifying {
		t.Fatalf("%+v %v", out, err)
	}
	if _, err = ExecuteOnce(s, p, pol, id); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("patches=%d", calls)
	}
}
func TestRestartIntentRejectsOperatorEdit(t *testing.T) {
	s, p, pol, id := safetyFixture(t)
	in, _ := s.GetIncident(id)
	snap, _, _ := p.Get(WantDeployment)
	if _, err := s.prepareIntent(in, pol, snap); err != nil {
		t.Fatal(err)
	}
	dep, _ := p.Client.AppsV1().Deployments(WantNamespace).Get(t.Context(), WantDeployment, metav1.GetOptions{})
	dep.Spec.Template.Spec.Containers[0].Image = "operator:third"
	p.Client.AppsV1().Deployments(WantNamespace).Update(t.Context(), dep, metav1.UpdateOptions{})
	if _, err := ExecuteOnce(s, p, pol, id); err == nil {
		t.Fatal("third-party template overwritten")
	}
	in, _ = s.GetIncident(id)
	if in.State != StEscalated {
		t.Fatal(in.State)
	}
}
func TestNoUnverifiedResolveEvenWithForgedEvent(t *testing.T) {
	s, _, _, id := safetyFixture(t)
	s.AppendEvent(id, "verified", `{"windows":3}`)
	if _, err := s.Transition(id, StResolved, "{}"); err == nil {
		t.Fatal("unverified resolve")
	}
}
func TestVerifyFractionalAndImpossibleCounts(t *testing.T) {
	for _, w := range []VerifyWindow{{Eligible: 100.9, Success: 99.8, FastOK: 99.8}, {Eligible: 100, Success: 101, FastOK: 100}, {Eligible: 100, Success: 100, FastOK: math.NaN()}} {
		ws := goodWindows()
		w.Start, w.End = ws[0].Start, ws[0].End
		ws[0] = w
		if VerifyRecovery(VerifyInput{DesiredPresent: true, GenerationHit: true, ReadyReplicas: 2, WantReplicas: 2, Windows: ws}).Recovered {
			t.Fatal("invalid counts passed")
		}
	}
}
func TestControllerLockExclusive(t *testing.T) {
	path := t.TempDir() + "/r.db"
	unlock, err := LockController(path)
	if err != nil {
		t.Fatal(err)
	}
	if other, err := LockController(path); err == nil {
		other()
		t.Fatal("second controller admitted")
	}
	unlock()
	other, err := LockController(path)
	if err != nil {
		t.Fatal(err)
	}
	other()
}

func TestExecutionRetryBoundPersisted(t *testing.T) {
	s, p, pol, id := safetyFixture(t)
	cs := p.Client.(*fake.Clientset)
	calls := 0
	cs.PrependReactor("patch", "deployments", func(kt.Action) (bool, runtime.Object, error) {
		calls++
		return true, nil, fmt.Errorf("transport failure")
	})
	for i := 0; i < 3; i++ {
		ExecuteOnce(s, p, pol, id)
	}
	in, _ := s.GetIncident(id)
	x, _ := s.Intent(id)
	if calls != 2 || x.Attempts != 2 || in.State != StEscalated {
		t.Fatalf("calls=%d attempts=%d state=%s", calls, x.Attempts, in.State)
	}
}
func TestNewTargetUIDCannotUseOldRegistration(t *testing.T) {
	s, p, pol, id := safetyFixture(t)
	dep, _ := p.Client.AppsV1().Deployments(WantNamespace).Get(t.Context(), WantDeployment, metav1.GetOptions{})
	dep.UID = "replacement"
	p.Client.AppsV1().Deployments(WantNamespace).Update(t.Context(), dep, metav1.UpdateOptions{})
	if _, err := ExecuteOnce(s, p, pol, id); err == nil {
		t.Fatal("old registration authorized replacement")
	}
}
func TestCancellationWinsAtomicVerification(t *testing.T) {
	s, p, pol, id := safetyFixture(t)
	if _, err := ExecuteOnce(s, p, pol, id); err != nil {
		t.Fatal(err)
	}
	x, _ := s.Intent(id)
	if _, err := s.Transition(id, StCancelled, "{}"); err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteVerification(id, x.Executed, "{}"); err == nil {
		t.Fatal("resolved cancellation")
	}
	ev, _ := s.Events(id)
	if hasKind(ev, "verified") {
		t.Fatal("orphan verification proof persisted")
	}
}

func TestQueuedProposalCannotBypassNewReservation(t *testing.T) {
	s, p, pol, id := safetyFixture(t)
	first, _ := s.GetIncident(id)
	second, _, err := s.CreateIncident("queued", first.TargetUID, first.Policy, first.Evidence)
	if err != nil {
		t.Fatal(err)
	}
	s.Transition(second.ID, StObserved, "{}")
	actions, _ := s.ActionsFor(id)
	s.RecordProposal(second.ID, EvalOutcome{Eligible: true, SnapshotHash: actions[0].Desired}, ActProposed)
	if _, err := ExecuteOnce(s, p, pol, id); err != nil {
		t.Fatal(err)
	}
	x, _ := s.Intent(id)
	if err := s.CompleteVerification(id, x.Executed, "{}"); err != nil {
		t.Fatal(err)
	}
	dep, _ := p.Client.AppsV1().Deployments(WantNamespace).Get(t.Context(), WantDeployment, metav1.GetOptions{})
	dep.Spec.Template.Spec.Containers[0].Image = "bad:next"
	p.Client.AppsV1().Deployments(WantNamespace).Update(t.Context(), dep, metav1.UpdateOptions{})
	if _, err := ExecuteOnce(s, p, pol, second.ID); err == nil {
		t.Fatal("queued proposal bypassed cooldown")
	}
}
