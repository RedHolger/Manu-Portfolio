// store.go — RecoverOps durable SQLite state (R1). Single-writer,
// WAL, schema embedded from migrations/recoverops/001_init.sql.
// Reopen preserves every row; callers must treat a persistence error as
// a 503 (never acknowledge an uncommitted alert as received).
package recoverops

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schemaDDL string

// Incident states legal in R1. R3 extends the machine; the transition
// table below is the single enforcement point.
const (
	StReceived  = "RECEIVED"
	StResolved  = "RESOLVED"
	StCancelled = "CANCELLED"
)

// legalR1 lists allowed transitions. Terminal states accept no outgoing
// edges: a late firing for a terminal occurrence must not reopen it.
var legalR1 = map[string]map[string]bool{
	StReceived: {StResolved: true, StCancelled: true},
}

// Incident is one remediation case.
type Incident struct {
	ID        string `json:"id"`
	SourceKey string `json:"source_key"`
	TargetUID string `json:"target_uid"`
	Policy    string `json:"policy_hash"`
	State     string `json:"state"`
	Evidence  string `json:"evidence"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// AlertRow is one persisted delivery occurrence.
type AlertRow struct {
	DeliveryID   string `json:"delivery_id"`
	OccurrenceID string `json:"occurrence_id"`
	IncidentID   string `json:"incident_id"`
	Firing       bool   `json:"firing"`
	StartsAt     string `json:"starts_at"`
	EndsAt       string `json:"ends_at"`
	ReceivedAt   string `json:"received_at"`
}

// Event is one journaled incident event (monotonic seq per incident).
type Event struct {
	Seq       int64  `json:"seq"`
	Timestamp string `json:"timestamp"`
	Kind      string `json:"kind"`
	Payload   string `json:"payload"`
}

// Store wraps the SQLite database.
type Store struct {
	db *sql.DB
}

// utcNow stamps rows in RFC3339Nano UTC.
func utcNow() string { return time.Now().UTC().Format(time.RFC3339Nano) }

// newID mints a 128-bit hex identity.
func newID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// Open creates/opens the database at path and applies the schema.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000;"); err != nil {
		_ = db.Close()
		return nil, err
	}
	if _, err := db.Exec(schemaDDL); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

// Close releases the database.
func (s *Store) Close() error { return s.db.Close() }

// Ping verifies the database answers (readiness).
func (s *Store) Ping() error {
	_, err := s.db.Exec("SELECT 1")
	return err
}

// CreateIncident inserts a RECEIVED incident with a unique source key.
// A duplicate source key returns the existing incident (no second case).
func (s *Store) CreateIncident(sourceKey, targetUID, policyHash, evidence string) (Incident, bool, error) {
	var in Incident
	tx, err := s.db.Begin()
	if err != nil {
		return in, false, err
	}
	defer tx.Rollback()
	row := tx.QueryRow(`SELECT id, source_key, target_uid, policy_hash, state,
		evidence, created_at, updated_at FROM incidents WHERE source_key=$1`, sourceKey)
	if err := row.Scan(&in.ID, &in.SourceKey, &in.TargetUID, &in.Policy,
		&in.State, &in.Evidence, &in.CreatedAt, &in.UpdatedAt); err == nil {
		return in, false, nil // duplicate source: return existing, no new row
	}
	now := utcNow()
	in = Incident{ID: newID(), SourceKey: sourceKey, TargetUID: targetUID,
		Policy: policyHash, State: StReceived, Evidence: evidence,
		CreatedAt: now, UpdatedAt: now}
	if _, err := tx.Exec(`INSERT INTO incidents(id, source_key, target_uid,
		policy_hash, state, evidence, created_at, updated_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8)`,
		in.ID, in.SourceKey, in.TargetUID, in.Policy, in.State,
		in.Evidence, in.CreatedAt, in.UpdatedAt); err != nil {
		return in, false, err
	}
	if err := appendEventTx(tx, in.ID, "created", evidence); err != nil {
		return in, false, err
	}
	if err := tx.Commit(); err != nil {
		return in, false, err
	}
	return in, true, nil
}

// GetIncident fetches one incident by ID.
func (s *Store) GetIncident(id string) (Incident, error) {
	var in Incident
	err := s.db.QueryRow(`SELECT id, source_key, target_uid, policy_hash, state,
		evidence, created_at, updated_at FROM incidents WHERE id=$1`, id).Scan(
		&in.ID, &in.SourceKey, &in.TargetUID, &in.Policy,
		&in.State, &in.Evidence, &in.CreatedAt, &in.UpdatedAt)
	return in, err
}

// ListIncidents returns newest-first with bounded pagination (1..100,
// default 20). Negative/zero limits are rejected by the caller.
func (s *Store) ListIncidents(limit, offset int64) ([]Incident, error) {
	if limit < 1 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := s.db.Query(`SELECT id, source_key, target_uid, policy_hash, state,
		evidence, created_at, updated_at FROM incidents
		ORDER BY created_at DESC LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Incident
	for rows.Next() {
		var in Incident
		if err := rows.Scan(&in.ID, &in.SourceKey, &in.TargetUID, &in.Policy,
			&in.State, &in.Evidence, &in.CreatedAt, &in.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, in)
	}
	return out, rows.Err()
}

// Transition moves an incident along a legal edge, journaling the move.
// Illegal edges (including out of terminal states) are rejected without
// writing anything.
func (s *Store) Transition(id, to, payload string) (Incident, error) {
	var in Incident
	tx, err := s.db.Begin()
	if err != nil {
		return in, err
	}
	defer tx.Rollback()
	row := tx.QueryRow(`SELECT id, source_key, target_uid, policy_hash, state,
		evidence, created_at, updated_at FROM incidents WHERE id=$1`, id)
	if err := row.Scan(&in.ID, &in.SourceKey, &in.TargetUID, &in.Policy,
		&in.State, &in.Evidence, &in.CreatedAt, &in.UpdatedAt); err != nil {
		return in, err
	}
	if !legalR1[in.State][to] {
		return in, fmt.Errorf("illegal transition %s -> %s", in.State, to)
	}
	now := utcNow()
	if _, err := tx.Exec(`UPDATE incidents SET state=$1, updated_at=$2 WHERE id=$3`,
		to, now, id); err != nil {
		return in, err
	}
	if err := appendEventTx(tx, id, "transition", payload); err != nil {
		return in, err
	}
	if err := tx.Commit(); err != nil {
		return in, err
	}
	in.State, in.UpdatedAt = to, now
	return in, nil
}

func appendEventTx(tx *sql.Tx, incidentID, kind, payload string) error {
	var seq sql.NullInt64
	if err := tx.QueryRow(`SELECT max(seq) FROM events WHERE incident_id=$1`,
		incidentID).Scan(&seq); err != nil {
		return err
	}
	next := int64(0)
	if seq.Valid {
		next = seq.Int64 + 1
	}
	_, err := tx.Exec(`INSERT INTO events(incident_id, seq, timestamp, kind, payload)
		VALUES($1,$2,$3,$4,$5)`, incidentID, next, utcNow(), kind, payload)
	return err
}

// AppendEvent journals one event with the next monotonic sequence.
func (s *Store) AppendEvent(incidentID, kind, payload string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := appendEventTx(tx, incidentID, kind, payload); err != nil {
		return err
	}
	return tx.Commit()
}

// Events returns the journaled events for one incident in sequence order.
func (s *Store) Events(incidentID string) ([]Event, error) {
	rows, err := s.db.Query(`SELECT seq, timestamp, kind, payload FROM events
		WHERE incident_id=$1 ORDER BY seq`, incidentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.Seq, &e.Timestamp, &e.Kind, &e.Payload); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// RecordDelivery persists one webhook occurrence delivery. The delivery ID
// is the primary key: an exact redelivery inserts nothing and reports
// duplicate=true. The caller links the occurrence to an incident first.
func (s *Store) RecordDelivery(deliveryID, occurrenceID, incidentID string, firing bool, startsAt, endsAt string) (bool, error) {
	res, err := s.db.Exec(`INSERT OR IGNORE INTO alert_events(delivery_id,
		occurrence_id, incident_id, firing, starts_at, ends_at, received_at)
		VALUES($1,$2,$3,$4,$5,$6,$7)`,
		deliveryID, occurrenceID, incidentID, boolToInt(firing), startsAt, endsAt, utcNow())
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n == 0, nil
}

// IncidentForOccurrence returns the incident linked to an occurrence ID,
// or sql.ErrNoRows when the occurrence is unknown.
func (s *Store) IncidentForOccurrence(occurrenceID string) (Incident, error) {
	var in Incident
	err := s.db.QueryRow(`SELECT i.id, i.source_key, i.target_uid, i.policy_hash,
		i.state, i.evidence, i.created_at, i.updated_at FROM incidents i
		JOIN alert_events a ON a.incident_id = i.id
		WHERE a.occurrence_id=$1 ORDER BY a.received_at DESC LIMIT 1`,
		occurrenceID).Scan(&in.ID, &in.SourceKey, &in.TargetUID, &in.Policy,
		&in.State, &in.Evidence, &in.CreatedAt, &in.UpdatedAt)
	return in, err
}

// RegisterGood stores a canonical known-good pod template for a target UID.
// The template is compacted to canonical JSON and hashed; the hash binds
// future rollbacks (R3) to exactly these bytes.
func (s *Store) RegisterGood(targetUID, templateJSON string) (string, error) {
	var compact interface{}
	if err := json.Unmarshal([]byte(templateJSON), &compact); err != nil {
		return "", fmt.Errorf("template is not JSON: %w", err)
	}
	raw, err := json.Marshal(compact)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	hash := fmt.Sprintf("%x", sum)
	now := utcNow()
	_, err = s.db.Exec(`INSERT INTO known_good(target_uid, template_json,
		template_hash, verified_at) VALUES($1,$2,$3,$4)
		ON CONFLICT(target_uid) DO UPDATE SET template_json=$2,
		template_hash=$3, verified_at=$4`,
		targetUID, string(raw), hash, now)
	if err != nil {
		return "", err
	}
	return hash, nil
}

// KnownGood fetches the registered template for a target UID.
func (s *Store) KnownGood(targetUID string) (templateJSON, hash, verifiedAt string, err error) {
	err = s.db.QueryRow(`SELECT template_json, template_hash, verified_at
		FROM known_good WHERE target_uid=$1`, targetUID).Scan(&templateJSON, &hash, &verifiedAt)
	return templateJSON, hash, verifiedAt, err
}

// SetMeta persists one operational key (e.g. mode). GetMeta reads it.
func (s *Store) SetMeta(k, v string) error {
	_, err := s.db.Exec(`INSERT INTO meta(k, v) VALUES($1,$2)
		ON CONFLICT(k) DO UPDATE SET v=$2`, k, v)
	return err
}

// GetMeta reads one operational key, or sql.ErrNoRows when absent.
func (s *Store) GetMeta(k string) (string, error) {
	var v string
	err := s.db.QueryRow(`SELECT v FROM meta WHERE k=$1`, k).Scan(&v)
	return v, err
}

// CountByState reports incident counts per state (controller metrics).
func (s *Store) CountByState() (map[string]int64, error) {
	rows, err := s.db.Query(`SELECT state, count(*) FROM incidents GROUP BY state`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var st string
		var n int64
		if err := rows.Scan(&st, &n); err != nil {
			return nil, err
		}
		out[st] = n
	}
	return out, rows.Err()
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
