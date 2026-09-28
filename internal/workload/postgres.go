// postgres.go — PostgresStore: real §4.1 transaction (integration path).
// The pg driver import lives in cmd/labapi (blank import) once the driver
// version is pinned; this file uses database/sql only.
package workload

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// PostgresStore implements Store with real SQL transactions.
type PostgresStore struct {
	db *sql.DB
}

// NewPostgresStore wraps an open *sql.DB (driver + version pinned at M0).
func NewPostgresStore(db *sql.DB) *PostgresStore { return &PostgresStore{db: db} }

func (p *PostgresStore) Reserve(ctx context.Context, req ReserveRequest) (Reservation, bool, error) {
	if err := req.Validate(); err != nil {
		return Reservation{}, false, err
	}
	hash := req.CanonicalHash()
	tx, err := p.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return Reservation{}, false, err
	}
	// SKU existence BEFORE the insert: otherwise the FK on reservations.sku
	// rejects unknown SKUs with a driver-specific 23503 error instead of
	// ErrUnknownSKU (live-found: unknown SKUs surfaced as 500s, and a 400
	// client_error population never materialized for SLI exclusion).
	var skuExists bool
	if serr := tx.QueryRowContext(ctx,
		`SELECT TRUE FROM inventory WHERE sku = $1`, req.SKU).Scan(&skuExists); serr != nil {
		_ = tx.Rollback()
		if errors.Is(serr, sql.ErrNoRows) {
			return Reservation{}, false, fmt.Errorf("%w: %s", ErrUnknownSKU, req.SKU)
		}
		return Reservation{}, false, serr
	}
	id := NewID()
	var insertedID string
	qerr := tx.QueryRowContext(ctx,
		`INSERT INTO reservations (id, idempotency_key, request_hash, sku, quantity)
		 VALUES ($1, $2, $3, $4, $5)
		 ON CONFLICT (idempotency_key) DO NOTHING
		 RETURNING id`,
		id, req.IdempotencyKey, hash, req.SKU, req.Quantity).Scan(&insertedID)
	if qerr != nil && !errors.Is(qerr, sql.ErrNoRows) {
		_ = tx.Rollback()
		return Reservation{}, false, qerr
	}
	if insertedID == "" {
		// Key exists: compare canonical hash, no decrement on replay.
		var existing Reservation
		rerr := tx.QueryRowContext(ctx,
			`SELECT id, idempotency_key, request_hash, sku, quantity
			 FROM reservations WHERE idempotency_key = $1`,
			req.IdempotencyKey).Scan(
			&existing.ID, &existing.IdempotencyKey, &existing.RequestHash,
			&existing.SKU, &existing.Quantity)
		_ = tx.Rollback() // read-only path, nothing to commit
		if rerr != nil {
			return Reservation{}, false, rerr
		}
		if existing.RequestHash != hash {
			return Reservation{}, false, ErrConflictingKey
		}
		return existing, true, nil
	}
	var avail int64
	uerr := tx.QueryRowContext(ctx,
		`UPDATE inventory SET available = available - $2
		 WHERE sku = $1 AND available >= $2
		 RETURNING available`,
		req.SKU, req.Quantity).Scan(&avail)
	if uerr != nil {
		_ = tx.Rollback()
		if errors.Is(uerr, sql.ErrNoRows) {
			var exists bool
			_ = p.db.QueryRowContext(ctx,
				`SELECT TRUE FROM inventory WHERE sku = $1`, req.SKU).Scan(&exists)
			if !exists {
				return Reservation{}, false, fmt.Errorf("%w: %s", ErrUnknownSKU, req.SKU)
			}
			return Reservation{}, false, fmt.Errorf("%w: %s", ErrInsufficientStock, req.SKU)
		}
		return Reservation{}, false, uerr
	}
	if cerr := tx.Commit(); cerr != nil {
		// Ambiguous: commit may have landed. Caller retries with same key.
		return Reservation{}, false, cerr
	}
	// Return insertedID (RETURNING), NOT the local hex string: PostgreSQL
	// normalizes UUID text to canonical dashed form on write, so the local
	// pre-image would mismatch every later read by key.
	return Reservation{
		ID: insertedID, IdempotencyKey: req.IdempotencyKey,
		RequestHash: hash, SKU: req.SKU, Quantity: req.Quantity,
	}, false, nil
}

func (p *PostgresStore) Get(ctx context.Context, id string) (Reservation, error) {
	var r Reservation
	err := p.db.QueryRowContext(ctx,
		`SELECT id, idempotency_key, request_hash, sku, quantity
		 FROM reservations WHERE id = $1`, id).Scan(
		&r.ID, &r.IdempotencyKey, &r.RequestHash, &r.SKU, &r.Quantity)
	return r, err
}

func (p *PostgresStore) Inventory(ctx context.Context, sku string) (int64, int64, error) {
	var init, avail int64
	err := p.db.QueryRowContext(ctx,
		`SELECT initial_stock, available FROM inventory WHERE sku = $1`, sku).Scan(&init, &avail)
	return init, avail, err
}

func (p *PostgresStore) ResetTestStock(ctx context.Context, sku string, stock int64) error {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM reservations WHERE sku = $1`, sku); err != nil {
		_ = tx.Rollback()
		return err
	}
	if _, err = tx.ExecContext(ctx,
		`INSERT INTO inventory (sku, initial_stock, available) VALUES ($1, $2, $2)
		 ON CONFLICT (sku) DO UPDATE SET initial_stock = $2, available = $2`, sku, stock); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}
