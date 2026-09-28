// Acceptance gates for §4.1 + correction #5. Run against MemStore always;
// PostgresStore runs the same suite under the `integration` build tag
// (needs Docker/Postgres; recorded as unexecuted until M0 unblocks).
package workload

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
)

func testStores(t *testing.T) map[string]Store {
	t.Helper()
	return map[string]Store{"mem": NewMemStore(100000)}
}

func TestUniqueReservationsDecrementOnce(t *testing.T) {
	for name, st := range testStores(t) {
		t.Run(name, func(t *testing.T) {
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
						IdempotencyKey: fmt.Sprintf("key-%d", i),
					})
				}(i)
			}
			wg.Wait()
			for i, err := range errs {
				if err != nil {
					t.Fatalf("req %d: %v", i, err)
				}
			}
			_, avail, err := st.Inventory(ctx, "demo-item")
			if err != nil {
				t.Fatal(err)
			}
			if avail != 0 {
				t.Fatalf("available=%d, want 0 after 100 unique × stock 100", avail)
			}
		})
	}
}

func TestDuplicateKeyDecrementsOnce(t *testing.T) {
	for name, st := range testStores(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			if err := st.ResetTestStock(ctx, "demo-item", 100); err != nil {
				t.Fatal(err)
			}
			var first Reservation
			var wg sync.WaitGroup
			errs := make([]error, 100)
			replays := make([]bool, 100)
			results := make([]Reservation, 100)
			for i := range errs {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					r, replayed, err := st.Reserve(ctx, ReserveRequest{
						SKU: "demo-item", Quantity: 1, IdempotencyKey: "same-key",
					})
					results[i], replays[i], errs[i] = r, replayed, err
				}(i)
			}
			wg.Wait()
			for i, err := range errs {
				if err != nil {
					t.Fatalf("req %d: %v", i, err)
				}
			}
			first = results[0]
			for i, r := range results {
				if r.ID != first.ID {
					t.Fatalf("req %d returned ID %s, want %s", i, r.ID, first.ID)
				}
			}
			_, avail, _ := st.Inventory(ctx, "demo-item")
			if avail != 99 {
				t.Fatalf("available=%d, want 99 (one decrement)", avail)
			}
		})
	}
}

func TestConflictingKeyRejected(t *testing.T) {
	for name, st := range testStores(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			if err := st.ResetTestStock(ctx, "demo-item", 100); err != nil {
				t.Fatal(err)
			}
			if _, _, err := st.Reserve(ctx, ReserveRequest{
				SKU: "demo-item", Quantity: 1, IdempotencyKey: "k",
			}); err != nil {
				t.Fatal(err)
			}
			_, _, err := st.Reserve(ctx, ReserveRequest{
				SKU: "demo-item", Quantity: 2, IdempotencyKey: "k",
			})
			if !errors.Is(err, ErrConflictingKey) {
				t.Fatalf("err=%v, want ErrConflictingKey", err)
			}
			_, avail, _ := st.Inventory(ctx, "demo-item")
			if avail != 99 {
				t.Fatalf("available=%d, want 99 (conflict decrements nothing)", avail)
			}
		})
	}
}

// TestAmbiguousWriteRetry (correction #5): commit lands but the response is
// lost; retrying the SAME key must return the ORIGINAL reservation with no
// second decrement.
func TestAmbiguousWriteRetry(t *testing.T) {
	st := NewMemStore(100000)
	ctx := context.Background()
	if err := st.ResetTestStock(ctx, "demo-item", 100); err != nil {
		t.Fatal(err)
	}
	calls := 0
	st.SetAfterCommitDrop(func() bool {
		calls++
		return calls == 1 // drop only the first response
	})
	_, _, firstErr := st.Reserve(ctx, ReserveRequest{
		SKU: "demo-item", Quantity: 3, IdempotencyKey: "ambiguous-1",
	})
	if firstErr == nil {
		t.Fatal("expected transport error on dropped response")
	}
	r, replayed, err := st.Reserve(ctx, ReserveRequest{
		SKU: "demo-item", Quantity: 3, IdempotencyKey: "ambiguous-1",
	})
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if !replayed {
		t.Fatal("retry should report replayed=true")
	}
	got, err := st.Get(ctx, r.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Quantity != 3 {
		t.Fatalf("quantity=%d, want 3", got.Quantity)
	}
	_, avail, _ := st.Inventory(ctx, "demo-item")
	if avail != 97 {
		t.Fatalf("available=%d, want 97 (single decrement of 3)", avail)
	}
}

func TestValidationBeforeWrite(t *testing.T) {
	st := NewMemStore(100000)
	ctx := context.Background()
	for _, req := range []ReserveRequest{
		{SKU: "", Quantity: 1, IdempotencyKey: "k"},
		{SKU: "demo-item", Quantity: 0, IdempotencyKey: "k"},
		{SKU: "demo-item", Quantity: -1, IdempotencyKey: "k"},
		{SKU: "demo-item", Quantity: 1, IdempotencyKey: ""},
	} {
		if _, _, err := st.Reserve(ctx, req); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("req %+v: err=%v, want ErrInvalidRequest", req, err)
		}
	}
	if _, _, err := st.Reserve(ctx, ReserveRequest{
		SKU: "nope", Quantity: 1, IdempotencyKey: "k2",
	}); !errors.Is(err, ErrUnknownSKU) {
		t.Fatalf("unknown sku: err=%v", err)
	}
	if _, _, err := st.Reserve(ctx, ReserveRequest{
		SKU: "demo-item", Quantity: 200000, IdempotencyKey: "k3",
	}); !errors.Is(err, ErrInsufficientStock) {
		t.Fatalf("stockout: err=%v", err)
	}
}
