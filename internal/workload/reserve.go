// Package workload implements the reservation transaction (§4.1).
//
// Algorithm (Postgres, read-committed, intentional):
//  1. INSERT reservation with ON CONFLICT DO NOTHING RETURNING id.
//  2. If inserted: UPDATE inventory SET available=available-$q
//     WHERE sku=$sku AND available >= $q RETURNING available.
//     No row → roll back → 409 insufficient stock (or 400 unknown SKU).
//  3. If key exists: read committed row, compare canonical request hash.
//     Identical replay → return existing ID (200, NO second decrement).
//     Different payload → 409 conflicting key.
//  4. Commit before responding. Commit-time connection loss = ambiguous
//     outcome; client retries with the SAME key (correction #5).
//
// Failure injection happens BEFORE the transaction, or AFTER COMMIT but
// before the response (ambiguous-outcome test only). No writes after a
// failed validation.
package workload

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
)

// Outcomes returned by Reserve.
var (
	ErrUnknownSKU        = errors.New("unknown sku")
	ErrInsufficientStock = errors.New("insufficient stock")
	ErrConflictingKey    = errors.New("conflicting idempotency key")
	ErrInvalidRequest    = errors.New("invalid request")
	ErrDependencyDown    = errors.New("dependency unavailable")
)

// Reservation is a committed row.
type Reservation struct {
	ID             string
	IdempotencyKey string
	RequestHash    string
	SKU            string
	Quantity       int64
}

// ReserveRequest is the validated client intent.
type ReserveRequest struct {
	SKU            string
	Quantity       int64
	IdempotencyKey string
}

// Validate rejects bad input before any storage access.
func (r ReserveRequest) Validate() error {
	if r.SKU == "" || r.IdempotencyKey == "" || r.Quantity <= 0 {
		return fmt.Errorf("%w: sku/key/quantity required", ErrInvalidRequest)
	}
	return nil
}

// CanonicalHash binds (sku, quantity); identical replay ⇒ identical hash.
func (r ReserveRequest) CanonicalHash() string {
	sum := sha256.Sum256([]byte(r.SKU + "\x00" + fmt.Sprint(r.Quantity)))
	return hex.EncodeToString(sum[:])
}

// Store abstracts persistence so unit tests run without PostgreSQL.
// PostgresStore (postgres.go) implements this with real transactions;
// MemStore (mem.go) implements it for unit tests with a mutex.
type Store interface {
	// Reserve executes the §4.1 algorithm. Returns (reservation, replayed,
	// error): replayed=true means the key already existed with an identical
	// hash (safe retry, no second decrement).
	Reserve(ctx context.Context, req ReserveRequest) (Reservation, bool, error)
	// Get returns a committed reservation by ID.
	Get(ctx context.Context, id string) (Reservation, error)
	// Inventory returns (initial, available) for oracle checks.
	Inventory(ctx context.Context, sku string) (initial, available int64, err error)
	// ResetTestStock sets stock for concurrency tests (test/reset path only,
	// never called from report or demo paths).
	ResetTestStock(ctx context.Context, sku string, stock int64) error
}
