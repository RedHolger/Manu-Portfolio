// pods_test.go — F3 adapter against the fake clientset (no cluster).
package faultlab

import (
	"context"
	"errors"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"
)

func labDeployment(uid string) *appsv1.Deployment {
	replicas := int32(2)
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "api-stable", Namespace: WantNamespace, UID: types.UID("dep-uid-1")},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "labapi", "slot": "stable"}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "labapi", "slot": "stable"}},
			},
		},
	}
}

func ownedPod(name, uid string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: name, Namespace: WantNamespace, UID: types.UID("pod-" + uid),
			Labels: map[string]string{"app": "labapi", "slot": "stable"},
			OwnerReferences: []metav1.OwnerReference{
				{APIVersion: "apps/v1", Kind: "Deployment", Name: "api-stable", UID: types.UID("dep-uid-1")},
			},
		},
	}
}

func testDeleter(t *testing.T, objs ...runtime.Object) (*PodDeleter, *fake.Clientset) {
	t.Helper()
	cs := fake.NewSimpleClientset(objs...)
	d, err := NewPodDeleter(cs, WantNamespace)
	if err != nil {
		t.Fatal(err)
	}
	d.PollWait = 3 * time.Second
	return d, cs
}

func TestNamespaceAndAllowlistEnforced(t *testing.T) {
	cs := fake.NewSimpleClientset()
	if _, err := NewPodDeleter(cs, "default"); err == nil {
		t.Fatal("foreign namespace accepted")
	}
	d, _ := testDeleter(t)
	if _, err := d.PickTarget(context.Background(), "postgres"); err == nil {
		t.Fatal("non-allowlisted deployment accepted")
	}
	if _, err := d.PickTarget(context.Background(), "api-stable"); err == nil {
		t.Fatal("missing deployment accepted")
	}
}

func TestPickOldestOwned(t *testing.T) {
	d, _ := testDeleter(t, labDeployment("x"), ownedPod("b", "2"), ownedPod("a", "1"),
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{
			Name: "stray", Namespace: WantNamespace,
			Labels: map[string]string{"app": "labapi", "slot": "stable"},
		}})
	got, err := d.PickTarget(context.Background(), "api-stable")
	if err != nil {
		t.Fatal(err)
	}
	if got.PodName != "a" || got.UID != "pod-1" {
		t.Fatalf("picked %+v, want pod a/pod-1 (stray must be ignored)", got)
	}
}

func TestDeleteThenVerifyGone(t *testing.T) {
	d, cs := testDeleter(t, labDeployment("x"), ownedPod("a", "1"), ownedPod("b", "2"))
	tgt, err := d.PickTarget(context.Background(), "api-stable")
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Delete(context.Background(), tgt); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := d.VerifyGone(context.Background(), tgt); err != nil {
		t.Fatalf("verify: %v", err)
	}
	// Sibling untouched.
	if _, err := cs.CoreV1().Pods(WantNamespace).Get(context.Background(), "b", metav1.GetOptions{}); err != nil {
		t.Fatalf("sibling deleted: %v", err)
	}
}

func TestLostResponseReconciled(t *testing.T) {
	d, cs := testDeleter(t, labDeployment("x"), ownedPod("a", "1"))
	// Simulate a lost delete response that DID land: reactor errors, but the
	// object is actually removed.
	cs.PrependReactor("delete", "pods", func(action ktesting.Action) (bool, runtime.Object, error) {
		da := action.(ktesting.DeleteAction)
		_ = cs.Tracker().Delete(action.GetResource(), action.GetNamespace(), da.GetName())
		return true, nil, errors.New("connection reset by peer")
	})
	tgt := PodTarget{Deployment: "api-stable", PodName: "a", UID: "pod-1"}
	if err := d.Delete(context.Background(), tgt); err != nil {
		t.Fatalf("lost-but-landed delete must reconcile to success: %v", err)
	}
}

func TestReplacementNeverTouched(t *testing.T) {
	d, cs := testDeleter(t, labDeployment("x"), ownedPod("a", "1"))
	// Precondition conflict: by the time we re-read, a replacement (new UID,
	// same name) exists. Delete must report success WITHOUT touching it.
	cs.PrependReactor("delete", "pods", func(action ktesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewConflict(
			schema.GroupResource{Resource: "pods"}, "a",
			errors.New("UID precondition failed"))
	})
	cs.PrependReactor("get", "pods", func(action ktesting.Action) (bool, runtime.Object, error) {
		ga := action.(ktesting.GetAction)
		if ga.GetName() == "a" {
			return true, ownedPod("a", "2"), nil // replacement UID
		}
		return false, nil, nil
	})
	tgt := PodTarget{Deployment: "api-stable", PodName: "a", UID: "pod-1"}
	if err := d.Delete(context.Background(), tgt); err != nil {
		t.Fatalf("replacement race must resolve to success: %v", err)
	}
	if err := d.VerifyGone(context.Background(), tgt); err != nil {
		t.Fatalf("replacement counts as gone-original: %v", err)
	}
}

func TestVerifyTimesOutWhenStuck(t *testing.T) {
	d, _ := testDeleter(t, labDeployment("x"), ownedPod("a", "1"))
	tgt := PodTarget{Deployment: "api-stable", PodName: "a", UID: "pod-1"}
	if err := d.VerifyGone(context.Background(), tgt); err == nil {
		t.Fatal("stuck pod must fail verification, not assume absence")
	}
}
