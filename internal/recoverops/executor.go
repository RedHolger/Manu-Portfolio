// executor.go — R3 one-rollback executor (offline logic + fake-tested).
// Live Kubernetes patching goes through the Patcher interface; unit tests
// use FakePatcher. No cluster mutation happens in this package's tests.
//
// Contract (§8 R3):
//   - Transactionally claim the incident action before mutation, persisting
//     current UID, resourceVersion, before hash and desired template hash.
//   - Patch with UID/resourceVersion preconditions, replacing only the pod
//     template. Never overwrite replicas or unrelated settings (enforced by
//     the patcher implementation, which sends a template-only patch).
//   - One logical desired-state action with possible request retries, never a
//     repeated rollback loop: action_key = incidentID + ":restore" is unique;
//     retries update the same row, never insert a second logical action.
//   - Cancellation cannot undo an accepted patch; it only stops future work.
package recoverops

import (
	"errors"
	"fmt"
)

// Sentinel patcher errors (mapped from k8s API errors by the live adapter).
var (
	ErrConflictUID      = errors.New("target UID changed (replacement)")
	ErrConflictTemplate = errors.New("operator conflict: live template differs from both before and desired")
	ErrConflictRV       = errors.New("resourceVersion conflict (status-only change suspected)")
	ErrTimeout          = errors.New("API timeout")
	ErrResponseLost     = errors.New("accepted but response lost")
)

// TargetSnapshot is the live Deployment identity read before mutation.
type TargetSnapshot struct {
	UID                string `json:"uid"`
	ResourceVersion    string `json:"resource_version"`
	TemplateHash       string `json:"template_hash"`
	Generation         int64  `json:"generation"`
	ObservedGeneration int64  `json:"observed_generation"`
	Updated            int64  `json:"updated_replicas"`
	Available          int64  `json:"available_replicas"`
	Total              int64  `json:"replicas"`
	Ready              int64  `json:"ready_replicas"`
	Want               int64  `json:"want_replicas"`
}

// Patcher applies the desired pod template under preconditions.
// accepted=true means the API accepted the patch (even if the response was
// then lost). Implementations must send UID + resourceVersion preconditions
// and a template-only body.
type Patcher interface {
	Get(namespace, deployment string) (TargetSnapshot, error)
	Patch(namespace, deployment, expectUID, expectRV, desiredHash string) (accepted bool, responseLost bool, err error)
}

// Decision is the executor's next step after a patch attempt or restart.
type Decision string

const (
	DecVerify  Decision = "verify"   // desired state observed or accepted; proceed to VERIFYING
	DecRetry   Decision = "retry"    // bounded conditional retry permitted
	DecEscalat Decision = "escalate" // concurrent change / replacement; do not overwrite
)

// ClaimForExecution transactionally claims the single logical action for
// incidentID and moves the incident toward EXECUTING. It records before/
// desired hashes plus the observed UID/resourceVersion so a restart can
// reconcile. Idempotent: re-claiming the same incident returns the same key
// without creating a second action row.
func (s *Store) ClaimForExecution(incidentID, uid, resourceVersion, beforeHash, desiredHash string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	key := incidentID + ":restore"
	now := utcNow()
	if _, err := tx.Exec(`INSERT OR IGNORE INTO actions(action_key,
		incident_id, before_hash, desired_hash, resource_version, status,
		error, created_at, updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$8)`,
		key, incidentID, beforeHash, desiredHash, resourceVersion, ActProposed, "", now); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE actions SET before_hash=$1, desired_hash=$2,
		resource_version=$3, status=$4, updated_at=$5
		WHERE action_key=$6 AND status IN ($7,$8)`,
		beforeHash, desiredHash, resourceVersion, StExecuting, now,
		key, ActProposed, StReconcil); err != nil {
		return err
	}
	if err := appendEventTx(tx, incidentID,
		"claim", fmt.Sprintf(`{"uid":%q,"rv":%q,"before":%.12q,"desired":%.12q}`,
			uid, resourceVersion, beforeHash, desiredHash)); err != nil {
		return err
	}
	return tx.Commit()
}

// MarkActionStatus sets the single logical action row to status (EXECUTING,
// VERIFYING, EXECUTED, FAILED) with an optional error string.
func (s *Store) MarkActionStatus(incidentID, status, errMsg string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	key := incidentID + ":restore"
	now := utcNow()
	if _, err := tx.Exec(`UPDATE actions SET status=$1, error=$2, updated_at=$3
		WHERE action_key=$4`, status, errMsg, now, key); err != nil {
		return err
	}
	if err := appendEventTx(tx, incidentID, "action-status",
		fmt.Sprintf(`{"status":%q,"error":%q}`, status, errMsg)); err != nil {
		return err
	}
	return tx.Commit()
}

// DecideOnPatchError maps a patch failure to retry/escalate without
// touching the cluster. Pure function for tests and the live reconciler.
func DecideOnPatchError(err error, live TargetSnapshot, claimedUID, beforeHash, desiredHash string) Decision {
	if err == nil {
		return DecVerify
	}
	if errors.Is(err, ErrConflictUID) || live.UID != claimedUID {
		return DecEscalat
	}
	if errors.Is(err, ErrConflictTemplate) {
		return DecEscalat
	}
	if live.TemplateHash != "" && live.TemplateHash != beforeHash && live.TemplateHash != desiredHash {
		return DecEscalat
	}
	if errors.Is(err, ErrConflictRV) || errors.Is(err, ErrTimeout) {
		return DecRetry
	}
	return DecEscalat
}

// DecideAfterRestart maps post-restart observation to the next step.
//   - desired present → continue verification (response may have been lost).
//   - old (before) present → one bounded conditional retry is permitted.
//   - third template or new UID → escalate, never overwrite.
//   - cancellation is handled by the caller: it stops future actions but
//     never undoes an accepted patch.
func DecideAfterRestart(live TargetSnapshot, claimedUID, beforeHash, desiredHash string) Decision {
	if live.UID != claimedUID {
		return DecEscalat
	}
	switch live.TemplateHash {
	case desiredHash:
		return DecVerify
	case beforeHash:
		return DecRetry
	default:
		return DecEscalat
	}
}

// DecideOnResponseLost handles accepted=true + response lost: the patch may
// or may not have landed, so re-read (caller supplies fresh live snapshot)
// and route through the restart decision. Never blindly re-patch.
func DecideOnResponseLost(live TargetSnapshot, claimedUID, beforeHash, desiredHash string) Decision {
	return DecideAfterRestart(live, claimedUID, beforeHash, desiredHash)
}
