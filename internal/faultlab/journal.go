// journal.go — durable SQLite journal (F1). Intent and cleanup targets are
// persisted BEFORE any mutation; phase changes commit transactionally with
// their event. A second process/host shares nothing: the service lock row
// serializes experiments per target service on one controller DB.
package faultlab

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// ErrLocked: another experiment holds the target service.
var ErrLocked = errors.New("target service locked by another experiment")

const journalDDL = `
CREATE TABLE IF NOT EXISTS runs(
  id TEXT PRIMARY KEY,
  scenario_hash TEXT NOT NULL,
  service TEXT NOT NULL,
  state TEXT NOT NULL,
  started_at TEXT NOT NULL,
  deadline_at TEXT NOT NULL,
  seed INTEGER NOT NULL,
  error TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS experiment_lock(
  service TEXT PRIMARY KEY,
  run_id TEXT NOT NULL,
  acquired_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS faults(
  id TEXT NOT NULL,
  run_id TEXT NOT NULL,
  kind TEXT NOT NULL,
  target_json TEXT NOT NULL,
  original_json TEXT NOT NULL DEFAULT '',
  expires_at TEXT NOT NULL,
  applied_at TEXT NOT NULL DEFAULT '',
  cleared_at TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (id, run_id)
);
CREATE TABLE IF NOT EXISTS events(
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  run_id TEXT NOT NULL,
  sequence INTEGER NOT NULL,
  timestamp TEXT NOT NULL,
  kind TEXT NOT NULL,
  payload_json TEXT NOT NULL DEFAULT '{}',
  UNIQUE (run_id, sequence)
);
`

// Run is a journaled experiment row.
type Run struct {
	ID           string
	ScenarioHash string
	Service      string
	State        string
	StartedAt    string
	DeadlineAt   string
	Seed         int64
	Error        string
}

// Journal wraps the SQLite DB.
type Journal struct {
	db *sql.DB
}

// Open creates/opens the journal at path and applies DDL.
func Open(path string) (*Journal, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // single-writer journal; serialized access
	if _, err := db.Exec("PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000;"); err != nil {
		_ = db.Close()
		return nil, err
	}
	if _, err := db.Exec(journalDDL); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Journal{db: db}, nil
}

// Close releases the DB.
func (j *Journal) Close() error { return j.db.Close() }

func nowUTC() string { return time.Now().UTC().Format(time.RFC3339Nano) }

// CreateRun persists a CREATED run and acquires the service lock in one
// transaction. A held lock fails the whole transaction (nothing partial).
func (j *Journal) CreateRun(id, scenarioHash, service string, seed int64, deadline time.Time) error {
	tx, err := j.db.Begin()
	if err != nil {
		return err
	}
	commit := func() error { return tx.Commit() }
	fail := func(e error) error {
		_ = tx.Rollback()
		return e
	}
	if _, err := tx.Exec(
		`INSERT INTO experiment_lock(service, run_id, acquired_at) VALUES(?,?,?)`,
		service, id, nowUTC()); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("%w: %s", ErrLocked, service)
	}
	if _, err := tx.Exec(
		`INSERT INTO runs(id, scenario_hash, service, state, started_at, deadline_at, seed)
		 VALUES(?,?,?,?,?,?,?)`,
		id, scenarioHash, service, StCreated, nowUTC(), deadline.UTC().Format(time.RFC3339Nano), seed); err != nil {
		return fail(err)
	}
	if _, err := tx.Exec(
		`INSERT INTO events(run_id, sequence, timestamp, kind) VALUES(?,0,?,'created')`,
		id, nowUTC()); err != nil {
		return fail(err)
	}
	return commit()
}

// RecordFault persists injection intent BEFORE the mutation is issued.
func (j *Journal) RecordFault(runID, faultID, kind, targetJSON, expiresAt string) error {
	_, err := j.db.Exec(
		`INSERT INTO faults(id, run_id, kind, target_json, expires_at) VALUES(?,?,?,?,?)`,
		faultID, runID, kind, targetJSON, expiresAt)
	return err
}

// MarkApplied records that the mutation was issued (cleanup target known).
func (j *Journal) MarkApplied(runID, faultID string) error {
	_, err := j.db.Exec(
		`UPDATE faults SET applied_at=? WHERE id=? AND run_id=?`, nowUTC(), faultID, runID)
	return err
}

// MarkCleared records verified cleanup of one fault.
func (j *Journal) MarkCleared(runID, faultID string) error {
	_, err := j.db.Exec(
		`UPDATE faults SET cleared_at=? WHERE id=? AND run_id=?`, nowUTC(), faultID, runID)
	return err
}

// Transition validates and persists a state change with its event, in one
// transaction. Reaching a terminal state releases the service lock in the
// same transaction (no orphaned locks on crash between statements).
// payload is the event body; errMsg (stored only when non-empty) is the
// terminal reason — the two are never conflated.
func (j *Journal) Transition(runID, from, to, eventKind, payload, errMsg string) error {
	if err := Next(from, to); err != nil {
		return err
	}
	tx, err := j.db.Begin()
	if err != nil {
		return err
	}
	fail := func(e error) error {
		_ = tx.Rollback()
		return e
	}
	res, err := tx.Exec(`UPDATE runs SET state=?, error=CASE WHEN ?!='' THEN ? ELSE error END
		WHERE id=? AND state=?`, to, errMsg, errMsg, runID, from)
	if err != nil {
		return fail(err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fail(err)
	}
	if n != 1 {
		return fail(fmt.Errorf("run %s not in %s (concurrent transition?)", runID, from))
	}
	var seq int
	if err := tx.QueryRow(`SELECT COALESCE(MAX(sequence),-1)+1 FROM events WHERE run_id=?`,
		runID).Scan(&seq); err != nil {
		return fail(err)
	}
	if _, err := tx.Exec(
		`INSERT INTO events(run_id, sequence, timestamp, kind, payload_json)
		 VALUES(?,?,?,?,?)`, runID, seq, nowUTC(), eventKind, payload); err != nil {
		return fail(err)
	}
	if Terminal(to) {
		if _, err := tx.Exec(`DELETE FROM experiment_lock WHERE run_id=?`, runID); err != nil {
			return fail(err)
		}
	}
	return tx.Commit()
}

// AppendEvent records an event WITHOUT a state change, so phase outcomes
// (cleanup status, recovery health, workload validity, oracle verdict) stay
// individually visible instead of being folded into the terminal reason.
// A failure here is returned: evidence writes are part of the experiment.
func (j *Journal) AppendEvent(runID, kind, payload string) error {
	if payload == "" {
		payload = "{}"
	}
	tx, err := j.db.Begin()
	if err != nil {
		return err
	}
	var seq int
	if err := tx.QueryRow(`SELECT COALESCE(MAX(sequence),-1)+1 FROM events WHERE run_id=?`,
		runID).Scan(&seq); err != nil {
		_ = tx.Rollback()
		return err
	}
	if _, err := tx.Exec(
		`INSERT INTO events(run_id, sequence, timestamp, kind, payload_json)
		 VALUES(?,?,?,?,?)`, runID, seq, nowUTC(), kind, payload); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// GetRun reads one run.
func (j *Journal) GetRun(runID string) (Run, error) {
	var r Run
	err := j.db.QueryRow(
		`SELECT id, scenario_hash, service, state, started_at, deadline_at, seed, error
		 FROM runs WHERE id=?`, runID).Scan(
		&r.ID, &r.ScenarioHash, &r.Service, &r.State,
		&r.StartedAt, &r.DeadlineAt, &r.Seed, &r.Error)
	return r, err
}

// Unfinished lists runs not in a terminal state (reconcile input).
func (j *Journal) Unfinished() ([]Run, error) {
	rows, err := j.db.Query(
		`SELECT id, scenario_hash, service, state, started_at, deadline_at, seed, error
		 FROM runs WHERE state NOT IN (?,?,?) ORDER BY started_at`,
		StPassed, StFailed, StCleanupF)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Run
	for rows.Next() {
		var r Run
		if err := rows.Scan(&r.ID, &r.ScenarioHash, &r.Service, &r.State,
			&r.StartedAt, &r.DeadlineAt, &r.Seed, &r.Error); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Faults lists a run's recorded faults (intent + cleanup state).
type FaultRow struct {
	ID, Kind, TargetJSON, ExpiresAt, AppliedAt, ClearedAt string
}

// Faults returns all fault rows for a run.
func (j *Journal) Faults(runID string) ([]FaultRow, error) {
	rows, err := j.db.Query(
		`SELECT id, kind, target_json, expires_at, applied_at, cleared_at
		 FROM faults WHERE run_id=? ORDER BY id`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FaultRow
	for rows.Next() {
		var f FaultRow
		if err := rows.Scan(&f.ID, &f.Kind, &f.TargetJSON, &f.ExpiresAt, &f.AppliedAt, &f.ClearedAt); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// EventCount returns the number of journaled events for a run.
func (j *Journal) EventCount(runID string) (int, error) {
	var n int
	err := j.db.QueryRow(`SELECT COUNT(*) FROM events WHERE run_id=?`, runID).Scan(&n)
	return n, err
}
