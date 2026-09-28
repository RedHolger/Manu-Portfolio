//go:build integration

// Postgres integration: same acceptance gates against real PostgreSQL
// (read-committed, ON CONFLICT, crash-safe commit). Requires a live server:
//
//	TEST_POSTGRES_DSN=postgres://lab:lab@127.0.0.1:5433/lab?sslmode=disable
//	go test -tags integration ./internal/workload/ -run TestPostgres -v
//
// The DSN database must have migrations/postgres/001_init.sql applied.
// MemStore-only runs are NOT sufficient evidence for these gates.
package workload

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func pgStore(t *testing.T) *PostgresStore {
	t.Helper()
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN unset — live Postgres gate unexecuted")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Ping(); err != nil {
		t.Fatalf("ping %s: %v", dsn, err)
	}
	// Cap test pool: 100 racing goroutines over a small pool still exercises
	// the transaction races without exhausting max_connections shared with
	// the running lab pods (live-found: FATAL too many clients).
	db.SetMaxOpenConns(10)
	return NewPostgresStore(db)
}

func TestPostgresConcurrentUnique(t *testing.T) {
	st := pgStore(t)
	ctx := context.Background()
	if err := st.ResetTestStock(ctx, "demo-item", 100); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make([]error, 100)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, _, errs[i] = st.Reserve(ctx, ReserveRequest{
				SKU: "demo-item", Quantity: 1,
				IdempotencyKey: fmt.Sprintf("pg-key-%d", i),
			})
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("req %d: %v", i, err)
		}
	}
	_, avail, _ := st.Inventory(ctx, "demo-item")
	if avail != 0 {
		t.Fatalf("available=%d, want 0", avail)
	}
}

func TestPostgresDuplicateAndConflict(t *testing.T) {
	st := pgStore(t)
	ctx := context.Background()
	if err := st.ResetTestStock(ctx, "demo-item", 100); err != nil {
		t.Fatal(err)
	}
	r1, replayed, err := st.Reserve(ctx, ReserveRequest{
		SKU: "demo-item", Quantity: 1, IdempotencyKey: "pg-dup",
	})
	if err != nil || replayed {
		t.Fatalf("first: %v replayed=%v", err, replayed)
	}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, rp, err := st.Reserve(ctx, ReserveRequest{
				SKU: "demo-item", Quantity: 1, IdempotencyKey: "pg-dup",
			})
			if err != nil || !rp || r.ID != r1.ID {
				t.Errorf("replay: %v %v %v", r, rp, err)
			}
		}()
	}
	wg.Wait()
	_, avail, _ := st.Inventory(ctx, "demo-item")
	if avail != 99 {
		t.Fatalf("available=%d, want 99", avail)
	}
	_, _, err = st.Reserve(ctx, ReserveRequest{
		SKU: "demo-item", Quantity: 2, IdempotencyKey: "pg-dup",
	})
	if !errors.Is(err, ErrConflictingKey) {
		t.Fatalf("conflict: %v", err)
	}
}

// Regression: unknown SKU must surface as ErrUnknownSKU (→ HTTP 400 →
// gateway client_error), not as a raw FK-violation 500. The FK on
// reservations.sku fires before the stock check; the explicit existence
// probe above is what makes the mapping correct.
func TestPostgresUnknownSKUIsClientError(t *testing.T) {
	st := pgStore(t)
	ctx := context.Background()
	_, _, err := st.Reserve(ctx, ReserveRequest{
		SKU: "no-such-sku", Quantity: 1, IdempotencyKey: "pg-badsku",
	})
	if !errors.Is(err, ErrUnknownSKU) {
		t.Fatalf("unknown sku: err=%v, want ErrUnknownSKU", err)
	}
}

func TestPostgresStockoutRollback(t *testing.T) {
	st := pgStore(t)
	ctx := context.Background()
	if err := st.ResetTestStock(ctx, "demo-item", 5); err != nil {
		t.Fatal(err)
	}
	// Drain exactly.
	for i := 0; i < 5; i++ {
		if _, _, err := st.Reserve(ctx, ReserveRequest{
			SKU: "demo-item", Quantity: 1,
			IdempotencyKey: fmt.Sprintf("pg-drain-%d", i),
		}); err != nil {
			t.Fatal(err)
		}
	}
	// Oversell attempt must roll back: 409, stock stays 0, no row written.
	_, _, err := st.Reserve(ctx, ReserveRequest{
		SKU: "demo-item", Quantity: 1, IdempotencyKey: "pg-oversell",
	})
	if !errors.Is(err, ErrInsufficientStock) {
		t.Fatalf("oversell: %v", err)
	}
	_, avail, _ := st.Inventory(ctx, "demo-item")
	if avail != 0 {
		t.Fatalf("available=%d, want 0 (rollback intact)", avail)
	}
	var n int
	db := st.db
	if err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM reservations WHERE idempotency_key='pg-oversell'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatal("oversell row leaked past rollback")
	}
}

func TestPostgresCommittedButUnacknowledged(t *testing.T) {
	st := pgStore(t)
	ctx := context.Background()
	if err := st.ResetTestStock(ctx, "demo-item", 100); err != nil {
		t.Fatal(err)
	}
	// Simulate: commit landed, response never reached the client.
	r1, replayed, err := st.Reserve(ctx, ReserveRequest{
		SKU: "demo-item", Quantity: 3, IdempotencyKey: "pg-ambig",
	})
	if err != nil || replayed {
		t.Fatalf("first: %v replayed=%v", err, replayed)
	}
	// Regression: the returned ID must equal the STORED id. PostgreSQL
	// normalizes UUID text to canonical dashed form, so returning the local
	// pre-write hex string mismatches every later read by key.
	if !strings.Contains(r1.ID, "-") {
		t.Fatalf("id %q is not canonical UUID form", r1.ID)
	}
	// Client retries with the SAME key: original ID, no second decrement.
	r2, replayed, err := st.Reserve(ctx, ReserveRequest{
		SKU: "demo-item", Quantity: 3, IdempotencyKey: "pg-ambig",
	})
	if err != nil || !replayed || r2.ID != r1.ID {
		t.Fatalf("retry: %v %v %v", r2, replayed, err)
	}
	got, err := st.Get(ctx, r1.ID)
	if err != nil || got.Quantity != 3 {
		t.Fatalf("get: %v %+v", err, got)
	}
	_, avail, _ := st.Inventory(ctx, "demo-item")
	if avail != 97 {
		t.Fatalf("available=%d, want 97", avail)
	}
}
