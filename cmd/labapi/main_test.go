// labapi dep-fault tests (F3): bounded pre-transaction 503s with lazy TTL.
package main

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"sre-portfolio/internal/workload"
)

func testServer() *server {
	return &server{
		store: workload.NewMemStore(100),
		log:   slog.New(slog.NewJSONHandler(os.Stderr, nil)),
		mode:  "healthy", dep: &depFault{},
	}
}

func postReserve(t *testing.T, srv *server, key string) int {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"sku": "demo-item", "quantity": 1})
	req := httptest.NewRequest("POST", "/v1/reservations", bytes.NewReader(body))
	req.Header.Set("Idempotency-Key", key)
	rec := httptest.NewRecorder()
	srv.handleReserve(rec, req)
	return rec.Code
}

func TestDepFaultBlocksThenExpires(t *testing.T) {
	srv := testServer()
	srv.dep.set(true, 50*time.Millisecond)
	if code := postReserve(t, srv, "k1"); code != http.StatusServiceUnavailable {
		t.Fatalf("code=%d, want 503 during fault", code)
	}
	// Nothing written by the blocked request.
	if _, avail, _ := srv.store.Inventory(t.Context(), "demo-item"); avail != 100 {
		t.Fatalf("avail=%d, blocked writes must not decrement", avail)
	}
	time.Sleep(80 * time.Millisecond) // past TTL: lazy expiry
	if code := postReserve(t, srv, "k2"); code != http.StatusCreated {
		t.Fatalf("code=%d, want 201 after TTL expiry", code)
	}
	srv.dep.set(false, 0)
	if code := postReserve(t, srv, "k3"); code != http.StatusCreated {
		t.Fatalf("code=%d, want 201 after disarm", code)
	}
}
