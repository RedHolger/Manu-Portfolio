// mem.go — in-memory Store with identical §4.1 semantics for unit tests.
// Mutex-guarded; concurrency tests (100 goroutines) run against this and,
// in integration, against PostgresStore.
package workload

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
)

type memSKU struct {
	initial   int64
	available int64
}

// MemStore mirrors Postgres semantics: insert-then-decrement, hash-compare
// on duplicate keys, no decrement on replay, 409 on conflicting payload.
type MemStore struct {
	mu              sync.Mutex
	inv             map[string]*memSKU
	byKey           map[string]Reservation
	byID            map[string]Reservation
	down            bool        // simulated dependency outage (pre-transaction 503)
	afterCommitDrop func() bool // ambiguous-outcome hook: commit OK, response lost
}

// NewMemStore seeds demo-item with the given stock.
func NewMemStore(stock int64) *MemStore {
	return &MemStore{
		inv:   map[string]*memSKU{"demo-item": {initial: stock, available: stock}},
		byKey: map[string]Reservation{},
		byID:  map[string]Reservation{},
	}
}

// SetDependencyDown flips the pre-transaction 503 fault.
func (m *MemStore) SetDependencyDown(down bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.down = down
}

// SetAfterCommitDrop installs the ambiguous-outcome hook (test only).
func (m *MemStore) SetAfterCommitDrop(f func() bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.afterCommitDrop = f
}

func (m *MemStore) Reserve(ctx context.Context, req ReserveRequest) (Reservation, bool, error) {
	if err := req.Validate(); err != nil {
		return Reservation{}, false, err
	}
	if err := ctx.Err(); err != nil {
		return Reservation{}, false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.down {
		return Reservation{}, false, ErrDependencyDown
	}
	hash := req.CanonicalHash()
	if existing, ok := m.byKey[req.IdempotencyKey]; ok {
		if existing.RequestHash != hash {
			return Reservation{}, false, ErrConflictingKey
		}
		return existing, true, nil // identical replay: no decrement
	}
	sku, ok := m.inv[req.SKU]
	if !ok {
		return Reservation{}, false, fmt.Errorf("%w: %s", ErrUnknownSKU, req.SKU)
	}
	if sku.available < req.Quantity {
		return Reservation{}, false, fmt.Errorf("%w: %s", ErrInsufficientStock, req.SKU)
	}
	r := Reservation{
		ID:             NewID(),
		IdempotencyKey: req.IdempotencyKey,
		RequestHash:    hash,
		SKU:            req.SKU,
		Quantity:       req.Quantity,
	}
	// Commit point: insert + decrement atomically under the mutex.
	sku.available -= req.Quantity
	m.byKey[req.IdempotencyKey] = r
	m.byID[r.ID] = r
	// Ambiguous-outcome simulation: committed, but response lost.
	if m.afterCommitDrop != nil && m.afterCommitDrop() {
		return Reservation{}, false, context.DeadlineExceeded // caller sees transport error
	}
	return r, false, nil
}

// NewID generates a 128-bit hex ID (stdlib only; DB uses UUID type).
func NewID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func (m *MemStore) Get(_ context.Context, id string) (Reservation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.byID[id]
	if !ok {
		return Reservation{}, fmt.Errorf("not found: %s", id)
	}
	return r, nil
}

func (m *MemStore) Inventory(_ context.Context, sku string) (int64, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.inv[sku]
	if !ok {
		return 0, 0, fmt.Errorf("%w: %s", ErrUnknownSKU, sku)
	}
	return s.initial, s.available, nil
}

func (m *MemStore) ResetTestStock(_ context.Context, sku string, stock int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.inv[sku] = &memSKU{initial: stock, available: stock}
	m.byKey = map[string]Reservation{}
	m.byID = map[string]Reservation{}
	return nil
}
