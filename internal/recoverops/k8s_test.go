package recoverops

import (
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"
)

func fakeDeployment(uid, rv, image string, replicas int32) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:            WantDeployment,
			Namespace:       WantNamespace,
			UID:             types.UID(uid),
			ResourceVersion: rv,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{Name: "api", Image: image}},
				},
			},
		},
	}
}

func TestLivePatchTemplateOnly(t *testing.T) {
	dep := fakeDeployment("uid-1", "rv-1", "bad:1", 2)
	cs := fake.NewSimpleClientset(dep)
	p, err := NewLivePatcher(cs, WantNamespace)
	if err != nil {
		t.Fatal(err)
	}
	snap, _, err := p.Get(WantDeployment)
	if err != nil {
		t.Fatal(err)
	}
	before := snap.TemplateHash
	desired := dep.Spec.Template
	desired.Spec.Containers[0].Image = "good:1"
	desiredRaw, desiredHash, err := TemplateHash(desired)
	if err != nil {
		t.Fatal(err)
	}
	after, err := p.PatchTemplate(WantDeployment, "uid-1", "rv-1", before, desiredRaw)
	if err != nil {
		t.Fatal(err)
	}
	if after.TemplateHash != desiredHash {
		t.Fatalf("template not restored: got %.12s want %.12s", after.TemplateHash, desiredHash)
	}
	got, _ := cs.AppsV1().Deployments(WantNamespace).Get(t.Context(), WantDeployment, metav1.GetOptions{})
	if *got.Spec.Replicas != 2 {
		t.Fatalf("replicas overwritten: %d", *got.Spec.Replicas)
	}
	if got.Spec.Template.Spec.Containers[0].Image != "good:1" {
		t.Fatalf("image not patched: %s", got.Spec.Template.Spec.Containers[0].Image)
	}
}

func TestLivePatchUIDConflict(t *testing.T) {
	dep := fakeDeployment("uid-2", "rv-1", "bad:1", 2)
	cs := fake.NewSimpleClientset(dep)
	p, _ := NewLivePatcher(cs, WantNamespace)
	_, err := p.PatchTemplate(WantDeployment, "uid-1", "rv-1", "before", `{"spec":{"containers":[{"name":"api","image":"good:1"}]}}`)
	if err == nil {
		t.Fatal("want UID conflict error")
	}
}

func TestLivePatchRejectsOtherDeployment(t *testing.T) {
	cs := fake.NewSimpleClientset()
	p, _ := NewLivePatcher(cs, WantNamespace)
	if _, _, err := p.Get("api-candidate"); err == nil {
		t.Fatal("want allowlist rejection")
	}
}
