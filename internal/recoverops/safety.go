package recoverops

import (
	"encoding/json"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
)

// Legacy executed rows still consume budget after upgrade. New intents reserve
// conservatively before mutation; no incident is counted in both branches.
const budgetReservationsSQL = `SELECT started_at FROM execution_intents WHERE target=?
UNION ALL SELECT a.updated_at FROM actions a JOIN incidents i ON i.id=a.incident_id
WHERE i.target_uid=? AND a.status='EXECUTED'
AND NOT EXISTS(SELECT 1 FROM execution_intents x WHERE x.incident_id=a.incident_id)`

// Intent is immutable across restarts; attempts are reserved before each API call.
type Intent struct {
	Incident string `json:"incident_id"`
	Target   string `json:"target"`
	UID      string `json:"uid"`
	Before   string `json:"before_hash"`
	Desired  string `json:"desired_hash"`
	Template string `json:"template_json"`
	RV       string `json:"resource_version"`
	Started  string `json:"started_at"`
	Executed string `json:"executed_at"`
	Attempts int    `json:"attempts"`
}

func (s *Store) Intent(id string) (Intent, error) {
	var x Intent
	err := s.db.QueryRow(`SELECT incident_id,target,uid,before_hash,desired_hash,template_json,resource_version,started_at,executed_at,attempts FROM execution_intents WHERE incident_id=?`, id).Scan(&x.Incident, &x.Target, &x.UID, &x.Before, &x.Desired, &x.Template, &x.RV, &x.Started, &x.Executed, &x.Attempts)
	return x, err
}

// RegisterBoundGood binds an explicitly verified template to an actual Deployment UID.
// Legacy unbound registrations remain readable but cannot authorize execution.
func (s *Store) RegisterBoundGood(target, uid, raw string) (string, error) {
	if target != WantNamespace+"/"+WantDeployment || uid == "" {
		return "", fmt.Errorf("invalid target binding")
	}
	var tmpl corev1.PodTemplateSpec
	if err := json.Unmarshal([]byte(raw), &tmpl); err != nil {
		return "", err
	}
	if len(tmpl.Spec.Containers) == 0 {
		return "", fmt.Errorf("template has no containers")
	}
	canonical, hash, err := TemplateHash(tmpl)
	if err != nil {
		return "", err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	var active int
	if err = tx.QueryRow(`SELECT count(*) FROM execution_intents x JOIN incidents i ON i.id=x.incident_id WHERE x.target=? AND i.state IN ('EXECUTING','RECONCILING','VERIFYING')`, target).Scan(&active); err != nil {
		return "", err
	}
	if active > 0 {
		return "", fmt.Errorf("cannot replace registration while recovery is active")
	}
	now := utcNow()
	if _, err = tx.Exec(`INSERT INTO known_good VALUES(?,?,?,?) ON CONFLICT(target_uid) DO UPDATE SET template_json=excluded.template_json,template_hash=excluded.template_hash,verified_at=excluded.verified_at`, target, canonical, hash, now); err != nil {
		return "", err
	}
	if _, err = tx.Exec(`INSERT INTO known_good_bindings VALUES(?,?,?,?) ON CONFLICT(target) DO UPDATE SET uid=excluded.uid,template_hash=excluded.template_hash,verified_at=excluded.verified_at`, target, uid, hash, now); err != nil {
		return "", err
	}
	return hash, tx.Commit()
}

// prepareIntent revalidates authorization and atomically reserves target ownership
// and budget. A stale proposal can never bypass a newer action's limits.
func (s *Store) prepareIntent(in Incident, pol Policy, live TargetSnapshot) (Intent, error) {
	var x Intent
	tx, err := s.db.Begin()
	if err != nil {
		return x, err
	}
	defer tx.Rollback()
	var state, policy, target, mode string
	if err = tx.QueryRow(`SELECT state,policy_hash,target_uid FROM incidents WHERE id=?`, in.ID).Scan(&state, &policy, &target); err != nil {
		return x, err
	}
	if state != StObserved && state != StEligible {
		return x, fmt.Errorf("incident not executable: %s", state)
	}
	if policy != pol.Hash || target != TargetUID(pol) {
		return x, fmt.Errorf("policy or target changed")
	}
	if err = tx.QueryRow(`SELECT v FROM meta WHERE k='mode'`).Scan(&mode); err != nil || mode != "enforce-lab" {
		return x, fmt.Errorf("execution requires persisted enforce-lab mode")
	}
	var status, proposedHash string
	if err = tx.QueryRow(`SELECT status,desired_hash FROM actions WHERE incident_id=?`, in.ID).Scan(&status, &proposedHash); err != nil || status != ActProposed {
		return x, fmt.Errorf("execution requires PROPOSED action")
	}
	var uid, hash, tmpl string
	if err = tx.QueryRow(`SELECT b.uid,b.template_hash,k.template_json FROM known_good_bindings b JOIN known_good k ON k.target_uid=b.target AND k.template_hash=b.template_hash WHERE b.target=?`, target).Scan(&uid, &hash, &tmpl); err != nil {
		return x, fmt.Errorf("known-good registration must be UID-bound: %w", err)
	}
	if uid != live.UID || hash != proposedHash {
		return x, fmt.Errorf("known-good UID or proposal hash mismatch")
	}
	if live.TemplateHash == hash {
		return x, fmt.Errorf("target already known-good; no action")
	}
	var evidence struct {
		StartsAt string `json:"startsAt"`
	}
	if err = json.Unmarshal([]byte(in.Evidence), &evidence); err != nil {
		return x, err
	}
	start, err := time.Parse(time.RFC3339Nano, evidence.StartsAt)
	if err != nil || time.Since(start) < 0 || time.Since(start) > MaxAlertAge {
		return x, fmt.Errorf("stale or absent degradation evidence")
	}
	var active int
	if err = tx.QueryRow(`SELECT count(*) FROM execution_intents x JOIN incidents i ON x.incident_id=i.id WHERE x.target=? AND i.state IN ('EXECUTING','RECONCILING','VERIFYING')`, target).Scan(&active); err != nil {
		return x, err
	}
	if active > 0 {
		return x, fmt.Errorf("target has active recovery")
	}
	rows, err := tx.Query(budgetReservationsSQL, target, target)
	if err != nil {
		return x, err
	}
	now := time.Now().UTC()
	count := int64(0)
	var last time.Time
	for rows.Next() {
		var ts string
		if err = rows.Scan(&ts); err != nil {
			rows.Close()
			return x, err
		}
		t, e := time.Parse(time.RFC3339Nano, ts)
		if e != nil {
			rows.Close()
			return x, e
		}
		if !t.Before(now.Add(-time.Hour)) {
			count++
		}
		if t.After(last) {
			last = t
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return x, err
	}
	if pol.PerIncident != 1 || pol.PerHour < 1 || pol.CooldownSecs < 1 {
		return x, fmt.Errorf("invalid execution limits")
	}
	if count >= pol.PerHour || (!last.IsZero() && now.Sub(last) < time.Duration(pol.CooldownSecs)*time.Second) {
		return x, fmt.Errorf("execution budget or cooldown exhausted")
	}
	x = Intent{Incident: in.ID, Target: target, UID: uid, Before: live.TemplateHash, Desired: hash, Template: tmpl, RV: live.ResourceVersion, Started: now.Format(time.RFC3339Nano)}
	if _, err = tx.Exec(`INSERT INTO execution_intents(incident_id,target,uid,before_hash,desired_hash,template_json,resource_version,started_at) VALUES(?,?,?,?,?,?,?,?)`, x.Incident, x.Target, x.UID, x.Before, x.Desired, x.Template, x.RV, x.Started); err != nil {
		return x, err
	}
	if _, err = tx.Exec(`UPDATE actions SET before_hash=?,desired_hash=?,resource_version=?,status=?,updated_at=? WHERE incident_id=?`, x.Before, x.Desired, x.RV, StExecuting, x.Started, x.Incident); err != nil {
		return x, err
	}
	if _, err = tx.Exec(`UPDATE incidents SET state=?,updated_at=? WHERE id=?`, StExecuting, x.Started, x.Incident); err != nil {
		return x, err
	}
	payload, _ := json.Marshal(x)
	if err = appendEventTx(tx, x.Incident, "claim", string(payload)); err != nil {
		return x, err
	}
	return x, tx.Commit()
}

func (s *Store) reserveAttempt(x Intent, rv string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`UPDATE execution_intents SET attempts=attempts+1,resource_version=? WHERE incident_id=? AND attempts=? AND attempts<2 AND EXISTS(SELECT 1 FROM incidents WHERE id=? AND state IN ('EXECUTING','RECONCILING'))`, rv, x.Incident, x.Attempts, x.Incident)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("attempt refused: cancelled, concurrent execution, or retry bound")
	}
	if err = appendEventTx(tx, x.Incident, "patch-attempt", fmt.Sprintf(`{"attempt":%d,"rv":%q}`, x.Attempts+1, rv)); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) markApplied(id string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := utcNow()
	res, err := tx.Exec(`UPDATE incidents SET state=?,updated_at=? WHERE id=? AND state IN ('EXECUTING','RECONCILING')`, StVerifying, now, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return fmt.Errorf("incident no longer executing")
	}
	if _, err = tx.Exec(`UPDATE execution_intents SET executed_at=? WHERE incident_id=? AND executed_at=''`, now, id); err != nil {
		return err
	}
	if _, err = tx.Exec(`UPDATE actions SET status=?,updated_at=? WHERE incident_id=?`, ActExecuted, now, id); err != nil {
		return err
	}
	if err = appendEventTx(tx, id, "applied", `{"next":"VERIFYING"}`); err != nil {
		return err
	}
	return tx.Commit()
}

// CompleteVerification commits proof and terminal state together. Cancellation
// or an intervening state change wins; it cannot leave a reusable proof behind.
func (s *Store) CompleteVerification(id, executedAt, payload string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var state, stamp string
	err = tx.QueryRow(`SELECT i.state,x.executed_at FROM incidents i JOIN execution_intents x ON x.incident_id=i.id WHERE i.id=?`, id).Scan(&state, &stamp)
	if err != nil {
		return err
	}
	if state != StVerifying || stamp != executedAt || stamp == "" {
		return fmt.Errorf("verification state or action changed")
	}
	if err = appendEventTx(tx, id, "verified", payload); err != nil {
		return err
	}
	if _, err = tx.Exec(`UPDATE incidents SET state=?,updated_at=? WHERE id=?`, StResolved, utcNow(), id); err != nil {
		return err
	}
	if err = appendEventTx(tx, id, "transition", `{"from":"VERIFYING","to":"RESOLVED","by":"server-verification"}`); err != nil {
		return err
	}
	return tx.Commit()
}

// PendingIncidents avoids starving older unfinished cases behind terminal rows.
func (s *Store) PendingIncidents() ([]Incident, error) {
	rows, err := s.db.Query(`SELECT id FROM incidents WHERE state NOT IN ('RESOLVED','ESCALATED','SUPPRESSED','CANCELLED') ORDER BY created_at LIMIT 100`)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	out := make([]Incident, 0, len(ids))
	for _, id := range ids {
		in, e := s.GetIncident(id)
		if e != nil {
			return nil, e
		}
		out = append(out, in)
	}
	return out, nil
}
