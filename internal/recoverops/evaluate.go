// evaluate.go — R2 policy evaluation for observe/enforce proposals.
// Evaluation is pure against durable state: target pins (from validated
// labels), alert freshness, registered known-good snapshot for the exact
// target UID, and execution limits read from the actions journal so a
// restart cannot reset them. Evaluation never mutates the cluster; in R2
// it never mutates anything but the local proposal records.
// The live "already known good, do not patch" comparison needs a cluster
// read and lands in R3 with the executor.
package recoverops

import (
	"fmt"
	"time"
)

// Evaluation states and action statuses introduced in R2.
const (
	StObserved    = "OBSERVED"
	ActObserved   = "OBSERVED"
	ActProposed   = "PROPOSED"
	ActExecuted   = "EXECUTED"
	ActFailed     = "FAILED"
	ActSuperseded = "SUPERSEDED"
)

// MaxAlertAge bounds degradation-evidence freshness: an alert older than
// this at evaluation time is stale and must not drive action.
const MaxAlertAge = 15 * time.Minute

// EvalOutcome is the result of evaluating one firing occurrence.
type EvalOutcome struct {
	Eligible     bool   `json:"eligible"`
	Reason       string `json:"reason"`
	SnapshotHash string `json:"snapshot_hash,omitempty"`
}

// TargetUID pins an occurrence to the policy target. In R2 the UID is the
// literal namespace/deployment (validated labels); R3 rebinds it to the
// cluster Deployment UID at snapshot time.
func TargetUID(pol Policy) string {
	return pol.Namespace + "/" + pol.Deployment
}

// Evaluate decides whether a firing occurrence may become a proposal.
// now is injected for deterministic tests.
func Evaluate(s *Store, pol Policy, labels map[string]string, startsAt string, now time.Time) EvalOutcome {
	for k, want := range pol.MatchLabels {
		if labels[k] != want {
			return EvalOutcome{Reason: fmt.Sprintf("label %s mismatch", k)}
		}
	}
	if labels["namespace"] != pol.Namespace || labels["deployment"] != pol.Deployment {
		return EvalOutcome{Reason: "target outside pinned namespace/deployment"}
	}
	starts, err := time.Parse(time.RFC3339, startsAt)
	if err != nil {
		return EvalOutcome{Reason: "unparseable startsAt"}
	}
	if age := now.Sub(starts); age < 0 || age > MaxAlertAge {
		return EvalOutcome{Reason: fmt.Sprintf("stale evidence (age %s)", age.Truncate(time.Second))}
	}
	target := TargetUID(pol)
	_, hash, _, err := s.KnownGood(target)
	if err != nil {
		return EvalOutcome{Reason: fmt.Sprintf("no known-good snapshot for %s", target)}
	}
	if ok, reason := checkLimits(s, pol, target, now); !ok {
		return EvalOutcome{Reason: reason}
	}
	return EvalOutcome{Eligible: true, Reason: "eligible", SnapshotHash: hash}
}

// checkLimits enforces the cooldown since the last execution and the
// per-hour execution budget — all from durable rows. The per-incident
// limit is structural: the proposal action key is incident-scoped, so one
// incident can never hold two logical actions.
func checkLimits(s *Store, pol Policy, target string, now time.Time) (bool, string) {
	last, count, err := s.ExecutedSince(target, now.Add(-time.Hour))
	if err != nil {
		return false, "limits unreadable"
	}
	if count >= pol.PerHour {
		return false, fmt.Sprintf("hourly budget exhausted (%d/%d)", count, pol.PerHour)
	}
	if !last.IsZero() && now.Sub(last) < time.Duration(pol.CooldownSecs)*time.Second {
		return false, fmt.Sprintf("cooldown active (last %s ago)", now.Sub(last).Truncate(time.Second))
	}
	return true, ""
}

// ExecutedSince returns the latest execution time at/after cut and the
// count of executions in [cut, now] for target chain bookkeeping. R2/R3
// record executions with status EXECUTED; proposals never consume budget.
func (s *Store) ExecutedSince(target string, cut time.Time) (time.Time, int64, error) {
	rows, err := s.db.Query(`SELECT a.created_at FROM actions a
		JOIN incidents i ON i.id = a.incident_id
		WHERE a.status=$1 AND i.target_uid=$2 AND a.created_at >= $3
		ORDER BY a.created_at DESC`, ActExecuted, target, cut.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return time.Time{}, 0, err
	}
	defer rows.Close()
	var last time.Time
	var n int64
	for rows.Next() {
		var ts string
		if err := rows.Scan(&ts); err != nil {
			return time.Time{}, 0, err
		}
		t, err := time.Parse(time.RFC3339Nano, ts)
		if err != nil {
			// Fall back to RFC3339 (tolerate both writers).
			t, err = time.Parse(time.RFC3339, ts)
			if err != nil {
				return time.Time{}, 0, err
			}
		}
		if last.IsZero() || t.After(last) {
			last = t
		}
		n++
	}
	return last, n, rows.Err()
}

// RecordProposal journals an evaluation outcome and, when eligible,
// creates the single per-incident proposal action (idempotent by key).
// actionStatus is OBSERVED (observe mode) or PROPOSED (enforce-lab, R3
// executes). Ineligible outcomes journal only the refusal.
func (s *Store) RecordProposal(incidentID string, out EvalOutcome, actionStatus string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	payload := fmt.Sprintf(`{"eligible":%v,"reason":%q,"snapshot":%q,"mode-status":%q}`,
		out.Eligible, out.Reason, out.SnapshotHash, actionStatus)
	if err := appendEventTx(tx, incidentID, "proposal", payload); err != nil {
		return err
	}
	if out.Eligible {
		key := incidentID + ":restore"
		now := utcNow()
		if _, err := tx.Exec(`INSERT OR IGNORE INTO actions(action_key,
			incident_id, before_hash, desired_hash, resource_version, status,
			error, created_at, updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$8)`,
			key, incidentID, "", out.SnapshotHash, "", actionStatus, "", now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ActionRow mirrors one actions row for reads.
type ActionRow struct {
	Key       string `json:"action_key"`
	Incident  string `json:"incident_id"`
	Desired   string `json:"desired_hash"`
	Status    string `json:"status"`
	Error     string `json:"error"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// ActionsFor lists the action rows of one incident (at most one logical
// action by key design, plus any superseded history in R3).
func (s *Store) ActionsFor(incidentID string) ([]ActionRow, error) {
	rows, err := s.db.Query(`SELECT action_key, incident_id, desired_hash,
		status, error, created_at, updated_at FROM actions
		WHERE incident_id=$1 ORDER BY created_at`, incidentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ActionRow
	for rows.Next() {
		var a ActionRow
		if err := rows.Scan(&a.Key, &a.Incident, &a.Desired, &a.Status,
			&a.Error, &a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
