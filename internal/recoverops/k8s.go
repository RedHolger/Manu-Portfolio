// k8s.go — R3 live Kubernetes adapter (lab only, enforce-lab only).
// Pinned to namespace sre-lab and the policy deployment (api-stable).
// Patch replaces ONLY spec.template under UID + resourceVersion
// preconditions; replicas and unrelated settings are never overwritten
// (we mutate a fresh GET copy's template field and Update with the
// expected resourceVersion for optimistic concurrency).
package recoverops

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// WantNamespace and WantDeployment pin every live mutation.
const (
	WantNamespace   = "sre-lab"
	WantDeployment  = "api-stable"
	WantContextName = "kind-sre-lab"
)

// LivePatcher applies template-only rollbacks against the lab cluster.
type LivePatcher struct {
	Client    kubernetes.Interface
	Namespace string
	Timeout   time.Duration
}

// NewLivePatcher enforces the dedicated namespace at construction.
func NewLivePatcher(client kubernetes.Interface, namespace string) (*LivePatcher, error) {
	if namespace != WantNamespace {
		return nil, fmt.Errorf("namespace %q != dedicated %q — refusing", namespace, WantNamespace)
	}
	return &LivePatcher{Client: client, Namespace: namespace, Timeout: 15 * time.Second}, nil
}

// TemplateHash canonicalizes a pod template to JSON and hashes it. Field
// order follows the Go struct definition (lab-grade canonicalization;
// the same function hashes both registered and live templates, so equality
// comparisons are consistent even if not RFC-compliant canonical JSON).
func TemplateHash(t corev1.PodTemplateSpec) (string, string, error) {
	raw, err := json.Marshal(t)
	if err != nil {
		return "", "", err
	}
	sum := sha256.Sum256(raw)
	return string(raw), fmt.Sprintf("%x", sum), nil
}

// GetLive reads the current UID, resourceVersion and template hash.
func (p *LivePatcher) Get(deployment string) (TargetSnapshot, string, error) {
	if deployment != WantDeployment {
		return TargetSnapshot{}, "", fmt.Errorf("deployment %q not allowlisted (api-stable only)", deployment)
	}
	ctx, cancel := context.WithTimeout(context.Background(), p.Timeout)
	defer cancel()
	dep, err := p.Client.AppsV1().Deployments(p.Namespace).Get(ctx, deployment, metav1.GetOptions{})
	if err != nil {
		return TargetSnapshot{}, "", err
	}
	_, hash, err := TemplateHash(dep.Spec.Template)
	if err != nil {
		return TargetSnapshot{}, "", err
	}
	return TargetSnapshot{
		UID:             string(dep.UID),
		ResourceVersion: dep.ResourceVersion,
		TemplateHash:    hash,
		Ready:           int64(dep.Status.ReadyReplicas),
		Want:            wantReplicas(dep.Spec.Replicas),
	}, hash, nil
}

// wantReplicas defaults nil to 1 (kubectl convention).

// PatchTemplate restores the desired pod template under preconditions.
// beforeHash is the template observed at claim time; desiredJSON/hash is the
// registered known-good template. Rules:
//   - UID mismatch → ErrConflictUID (target replacement: escalate).
//   - live hash not in {before, desired} → ErrConflictTemplate (operator edit).
//   - RV mismatch with UID+template otherwise intact → proceed once (the
//     caller treats status-only RV drift as one bounded retry).
func (p *LivePatcher) PatchTemplate(deployment, expectUID, expectRV, beforeHash, desiredJSON string) (TargetSnapshot, error) {
	if deployment != WantDeployment {
		return TargetSnapshot{}, fmt.Errorf("deployment %q not allowlisted", deployment)
	}
	var desired corev1.PodTemplateSpec
	if err := json.Unmarshal([]byte(desiredJSON), &desired); err != nil {
		return TargetSnapshot{}, fmt.Errorf("desired template is not JSON: %w", err)
	}
	_, desiredHash, err := TemplateHash(desired)
	if err != nil {
		return TargetSnapshot{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), p.Timeout)
	defer cancel()
	deps := p.Client.AppsV1().Deployments(p.Namespace)
	live, err := deps.Get(ctx, deployment, metav1.GetOptions{})
	if err != nil {
		return TargetSnapshot{}, err
	}
	if string(live.UID) != expectUID {
		return TargetSnapshot{}, ErrConflictUID
	}
	_, liveHash, err := TemplateHash(live.Spec.Template)
	if err != nil {
		return TargetSnapshot{}, err
	}
	if liveHash != beforeHash && !hashEquals(liveHash, desiredHash) {
		// Live template is a third value: concurrent operator change.
		return TargetSnapshot{}, ErrConflictTemplate
	}
	// Template-only mutation on a fresh copy; replicas untouched.
	live.Spec.Template = desired
	live.ResourceVersion = expectRV // optimistic-concurrency precondition
	updated, err := deps.Update(ctx, live, metav1.UpdateOptions{})
	if err != nil {
		return TargetSnapshot{}, err
	}
	_, newHash, err := TemplateHash(updated.Spec.Template)
	if err != nil {
		return TargetSnapshot{}, err
	}
	return TargetSnapshot{
		UID:             string(updated.UID),
		ResourceVersion: updated.ResourceVersion,
		TemplateHash:    newHash,
		Ready:           int64(updated.Status.ReadyReplicas),
		Want:            wantReplicas(updated.Spec.Replicas),
	}, nil
}

func hashEquals(a, b string) bool { return a == b }

func wantReplicas(r *int32) int64 {
	if r == nil {
		return 1
	}
	return int64(*r)
}

// Ensure appsv1 import is used (Deployment type reference for docs).
var _ = appsv1.Deployment{}
