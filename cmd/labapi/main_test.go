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

// Committed-but-response-lost over real HTTP: the first POST commits then
// the connection dies with no reply (ambiguous outcome); retrying the SAME
// key must return the ORIGINAL reservation with no second decrement.
// Ordinary successful replay alone does not cover this case.
func TestCommittedButResponseLostEndToEnd(t *testing.T) {
	srv := testServer()
	srv.testHooks = true
	s := httptest.NewServer(buildMux(srv))
	defer s.Close()
	post := func(key string, drop bool) (int, string, error) {
		body, _ := json.Marshal(map[string]any{"sku": "demo-item", "quantity": 1})
		req, _ := http.NewRequest("POST", s.URL+"/v1/reservations", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", key)
		if drop {
			req.Header.Set("X-Test-Drop-Response", "1")
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return 0, "", err
		}
		defer resp.Body.Close()
		var out map[string]string
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return resp.StatusCode, out["id"], nil
	}
	// 1. Commit lands, reply lost: client sees a transport error, no ID.
	if _, _, err := post("ambig-e2e", true); err == nil {
		t.Fatal("expected transport error on dropped response")
	}
	// 2. Same-key retry: original ID, no second decrement.
	code, id, err := post("ambig-e2e", false)
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("retry code=%d", code)
	}
	if id == "" {
		t.Fatal("retry returned no ID")
	}
	if _, avail, _ := srv.store.Inventory(t.Context(), "demo-item"); avail != 99 {
		t.Fatalf("avail=%d, want exactly one decrement of 100", avail)
	}
	// 3. The reservation is retrievable by the returned ID.
	if _, _, err := post("ambig-e2e", false); err != nil {
		t.Fatal(err)
	}
	resp, err := http.Get(s.URL + "/v1/reservations/" + id)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get code=%d", resp.StatusCode)
	}
}
