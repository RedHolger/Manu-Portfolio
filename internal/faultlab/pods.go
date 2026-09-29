// pods.go — F3 pod-delete adapter (offline: fake clientset; live: in-kind).
// Hard restrictions: dedicated namespace only, allowlisted Deployments only
// (api-stable, api-candidate — never gateway/postgres/prometheus/grafana),
// exactly one pod per call, deleted by UID precondition. A replacement pod
// reusing the name must NEVER be touched: identity is the UID, not the name.
package faultlab

import (
	"context"
	"fmt"
	"sort"
	"time"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
)

// AllowedPodTargets bounds pod deletion to the lab API slots.
var AllowedPodTargets = map[string]bool{"api-stable": true, "api-candidate": true}

// PodTarget identifies one pod by UID (not name).
type PodTarget struct {
	Deployment string
	PodName    string
	UID        string
}

// PodDeleter deletes lab pods with UID preconditions.
type PodDeleter struct {
	Client    kubernetes.Interface
	Namespace string
	PollWait  time.Duration // per-verify budget; default 60s
}

// NewPodDeleter enforces the dedicated namespace at construction.
func NewPodDeleter(client kubernetes.Interface, namespace string) (*PodDeleter, error) {
	if namespace != WantNamespace {
		return nil, fmt.Errorf("namespace %q != dedicated %q — refusing", namespace, WantNamespace)
	}
	return &PodDeleter{Client: client, Namespace: namespace, PollWait: 60 * time.Second}, nil
}

// PickTarget resolves exactly one pod owned by the allowlisted Deployment.
// Deterministic (oldest-first by name); zero candidates is an error, never
// a wildcard deletion.
func (p *PodDeleter) PickTarget(ctx context.Context, deployment string) (PodTarget, error) {
	if !AllowedPodTargets[deployment] {
		return PodTarget{}, fmt.Errorf("deployment %q not in allowlist (api-stable|api-candidate)", deployment)
	}
	dep, err := p.Client.AppsV1().Deployments(p.Namespace).Get(ctx, deployment, metav1.GetOptions{})
	if err != nil {
		return PodTarget{}, fmt.Errorf("get deployment: %w", err)
	}
	sel, err := metav1.LabelSelectorAsSelector(dep.Spec.Selector)
	if err != nil {
		return PodTarget{}, fmt.Errorf("bad deployment selector: %w", err)
	}
	// Pods are owned by ReplicaSets, which are owned by the Deployment:
	// resolve the transitive owner set (Deployment UID + its ReplicaSets).
	ownedUIDs := map[string]bool{string(dep.UID): true}
	rss, err := p.Client.AppsV1().ReplicaSets(p.Namespace).List(ctx, metav1.ListOptions{LabelSelector: sel.String()})
	if err != nil {
		return PodTarget{}, fmt.Errorf("list replicasets: %w", err)
	}
	for _, rs := range rss.Items {
		for _, ref := range rs.OwnerReferences {
			if ref.UID == dep.UID {
				ownedUIDs[string(rs.UID)] = true
			}
		}
	}
	pods, err := p.Client.CoreV1().Pods(p.Namespace).List(ctx, metav1.ListOptions{LabelSelector: sel.String()})
	if err != nil {
		return PodTarget{}, fmt.Errorf("list pods: %w", err)
	}
	var owned []v1.Pod
	for _, pod := range pods.Items {
		for _, ref := range pod.OwnerReferences {
			if ownedUIDs[string(ref.UID)] {
				owned = append(owned, pod)
				break
			}
		}
	}
	if len(owned) == 0 {
		return PodTarget{}, fmt.Errorf("no pods owned by %s (nothing to delete)", deployment)
	}
	sort.Slice(owned, func(i, j int) bool { return owned[i].Name < owned[j].Name })
	return PodTarget{Deployment: deployment, PodName: owned[0].Name, UID: string(owned[0].UID)}, nil
}

// Delete removes the target pod with a UID precondition, then reconciles a
// lost response: 404 means gone (success); a present pod with a DIFFERENT
// UID means the original is gone and the replacement must NOT be touched
// (success); same UID still present means the delete did not land (error,
// caller retries bounded with a FRESH resolve — never re-delete blind).
func (p *PodDeleter) Delete(ctx context.Context, target PodTarget) error {
	if !AllowedPodTargets[target.Deployment] {
		return fmt.Errorf("deployment %q not allowlisted", target.Deployment)
	}
	uid := types.UID(target.UID)
	err := p.Client.CoreV1().Pods(p.Namespace).Delete(ctx, target.PodName, metav1.DeleteOptions{
		Preconditions: &metav1.Preconditions{UID: &uid},
	})
	if err == nil {
		return nil
	}
	if !errors.IsNotFound(err) && !errors.IsConflict(err) {
		// Transport/unknown error: the mutation may or may not have landed.
		// Reconcile by reading current state instead of guessing.
		return p.reconcileAfterError(ctx, target, fmt.Errorf("delete: %w", err))
	}
	return p.reconcileAfterError(ctx, target, nil)
}

func (p *PodDeleter) reconcileAfterError(ctx context.Context, target PodTarget, derr error) error {
	got, err := p.Client.CoreV1().Pods(p.Namespace).Get(ctx, target.PodName, metav1.GetOptions{})
	if errors.IsNotFound(err) {
		return nil // gone (deleted, or never existed after all)
	}
	if err != nil {
		if derr != nil {
			return derr
		}
		return fmt.Errorf("re-read: %w", err)
	}
	if string(got.UID) != target.UID {
		return nil // replacement pod: original gone, do NOT touch it
	}
	if derr != nil {
		return derr // same UID present: delete did not land, bounded retry by caller
	}
	return fmt.Errorf("pod %s still present with same UID", target.PodName)
}

// VerifyGone polls until the original UID is gone (NotFound or replaced) or
// the budget expires. Absence of evidence is reported, never assumed.
func (p *PodDeleter) VerifyGone(ctx context.Context, target PodTarget) error {
	wait := p.PollWait
	if wait <= 0 {
		wait = 60 * time.Second
	}
	deadline := time.Now().Add(wait)
	for {
		got, err := p.Client.CoreV1().Pods(p.Namespace).Get(ctx, target.PodName, metav1.GetOptions{})
		if errors.IsNotFound(err) {
			return nil
		}
		if err == nil && string(got.UID) != target.UID {
			return nil // replaced: original gone
		}
		if time.Now().After(deadline) {
			if err != nil {
				return fmt.Errorf("verify: %w", err)
			}
			return fmt.Errorf("pod %s still present with original UID after %v", target.PodName, wait)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}
