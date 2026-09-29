// oracle.go — F4 reservation correctness oracle (offline; live PG acceptance
// deferred). Checks workload invariants for one SKU within a test run — not
// a general linearizability checker, not a consensus proof.
package faultlab

import (
	"context"
	"database/sql"
	"fmt"
)

// OpRecord is one logical operation's client-observed outcome.
type OpRecord struct {
	OpID   string `json:"op_id"`
	Key    string `json:"key"`
	SKU    string `json:"sku"`
	Qty    int64  `json:"qty"`
	Acked  bool   `json:"acked"`
	ResvID string `json:"reservation_id,omitempty"`
}

// ReservationRow is one committed ledger row.
type ReservationRow struct {
	ID   string `json:"id"`
	Key  string `json:"key"`
	Hash string `json:"hash"`
	SKU  string `json:"sku"`
	Qty  int64  `json:"qty"`
}

// Ledger abstracts the committed state (PG live, fake in tests).
type Ledger interface {
	Inventory(ctx context.Context, sku string) (initial, available int64, err error)
	Reservations(ctx context.Context, sku string) ([]ReservationRow, error)
}

// Findings separates violations (invariant breaks) from informational notes
// (ambiguous outcomes reconciled without violation).
type Findings struct {
	Violations []string
	Info       []string
	Committed  int // ledger rows
	Acked      int // acknowledged ops
	Ambiguous  int // unacked ops reconciled to commits
}

// Clean reports no violations (info notes may exist).
func (f Findings) Clean() bool { return len(f.Violations) == 0 }

// Check verifies, for one SKU:
//  1. conservation: available + Σcommitted == initial.
//  2. no negative stock; at most one row per idempotency key.
//  3. every acknowledged op resolves to a matching committed reservation
//     (by ID, or by key for commit-then-timeout ambiguity).
//  4. unacknowledged ops keyed in the ledger are committed-but-unacknowledged
//     (info); unkeyed ones failed-before-commit (info). Retries of one key
//     must agree on the reservation.
//  5. same key with different payloads acknowledged twice is a conflicting
//     double effect (violation).
func Check(ctx context.Context, l Ledger, sku string, ops []OpRecord) (Findings, error) {
	var f Findings
	if err := ctx.Err(); err != nil {
		return f, err
	}
	init, avail, err := l.Inventory(ctx, sku)
	if err != nil {
		return f, fmt.Errorf("inventory: %w", err)
	}
	rows, err := l.Reservations(ctx, sku)
	if err != nil {
		return f, fmt.Errorf("reservations: %w", err)
	}
	f.Committed = len(rows)
	if avail < 0 {
		f.Violations = append(f.Violations, fmt.Sprintf("negative stock: %d", avail))
	}
	var sum int64
	byKey := map[string][]ReservationRow{}
	for _, r := range rows {
		sum += r.Qty
		byKey[r.Key] = append(byKey[r.Key], r)
	}
	if avail+sum != init {
		f.Violations = append(f.Violations,
			fmt.Sprintf("conservation: available=%d committed=%d initial=%d", avail, sum, init))
	}
	for key, rs := range byKey {
		if len(rs) > 1 {
			f.Violations = append(f.Violations,
				fmt.Sprintf("duplicate key %q: %d rows", key, len(rs)))
		}
	}
	byID := map[string]ReservationRow{}
	for _, r := range rows {
		byID[r.ID] = r
	}
	seenKey := map[string]OpRecord{}
	for _, op := range ops {
		if op.Acked {
			f.Acked++
		}
		if prev, dup := seenKey[op.Key]; dup {
			// Same logical key seen twice: payload and outcome must agree.
			if prev.SKU != op.SKU || prev.Qty != op.Qty {
				if op.Acked && prev.Acked && op.ResvID != prev.ResvID {
					f.Violations = append(f.Violations,
						fmt.Sprintf("conflicting payloads under key %q acknowledged as %q and %q",
							op.Key, prev.ResvID, op.ResvID))
				} else if op.Acked && prev.Acked {
					f.Info = append(f.Info,
						fmt.Sprintf("key %q replayed with conflicting payload (rejected, no new effect)", op.Key))
				}
				continue
			}
			if op.ResvID != "" && prev.ResvID != "" && op.ResvID != prev.ResvID {
				f.Violations = append(f.Violations,
					fmt.Sprintf("key %q resolved to two reservations %q and %q",
						op.Key, prev.ResvID, op.ResvID))
				continue
			}
		} else {
			seenKey[op.Key] = op
		}
		if !op.Acked {
			if _, ok := byKey[op.Key]; ok {
				f.Ambiguous++
				f.Info = append(f.Info,
					fmt.Sprintf("op %s unacknowledged but key committed (ambiguous, reconciled)", op.OpID))
			} else {
				f.Info = append(f.Info,
					fmt.Sprintf("op %s unacknowledged, key absent (failed before commit)", op.OpID))
			}
			continue
		}
		// Acknowledged: must resolve to a matching committed row.
		if op.ResvID != "" {
			row, ok := byID[op.ResvID]
			if !ok {
				f.Violations = append(f.Violations,
					fmt.Sprintf("op %s acknowledged %q: no such reservation (lost write)", op.OpID, op.ResvID))
				continue
			}
			if row.SKU != op.SKU || row.Qty != op.Qty {
				f.Violations = append(f.Violations,
					fmt.Sprintf("op %s payload mismatch vs committed row", op.OpID))
			}
			continue
		}
		if _, ok := byKey[op.Key]; !ok {
			f.Violations = append(f.Violations,
				fmt.Sprintf("op %s acknowledged without ID and key absent (lost write)", op.OpID))
		} else {
			f.Info = append(f.Info,
				fmt.Sprintf("op %s acknowledged without ID, reconciled by key", op.OpID))
		}
	}
	return f, nil
}

// PGLedger reads the real lab schema (live acceptance, deferred).
type PGLedger struct {
	DB *sql.DB
}

// NewPGLedger wraps an open DB (pgx driver registered by the caller binary).
func NewPGLedger(db *sql.DB) *PGLedger { return &PGLedger{DB: db} }

// Inventory implements Ledger.
func (p *PGLedger) Inventory(ctx context.Context, sku string) (int64, int64, error) {
	var init, avail int64
	err := p.DB.QueryRowContext(ctx,
		`SELECT initial_stock, available FROM inventory WHERE sku=$1`, sku).Scan(&init, &avail)
	return init, avail, err
}

// Reservations implements Ledger.
func (p *PGLedger) Reservations(ctx context.Context, sku string) ([]ReservationRow, error) {
	rows, err := p.DB.QueryContext(ctx,
		`SELECT id, idempotency_key, request_hash, sku, quantity FROM reservations WHERE sku=$1`, sku)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ReservationRow
	for rows.Next() {
		var r ReservationRow
		if err := rows.Scan(&r.ID, &r.Key, &r.Hash, &r.SKU, &r.Qty); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
