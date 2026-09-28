// gateway_test.go — F2 injector contract against a fake admin server.
package faultlab

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fakeAdmin emulates the gateway admin surface (bounded faults, idempotent
// deletes, live-only state).
type fakeAdmin struct {
	mu     sync.Mutex
	faults map[string]map[string]any
}

func newFakeAdmin() (*fakeAdmin, *httptest.Server) {
	fa := &fakeAdmin{faults: map[string]map[string]any{}}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		fa.mu.Lock()
		defer fa.mu.Unlock()
		p := r.URL.Path
		switch {
		case r.Method == "PUT" && strings.HasPrefix(p, "/admin/faults/"):
			var b map[string]any
			if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			ttl, _ := b["ttlSeconds"].(float64)
			if ttl <= 0 || ttl > 120 {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":"ttl"}`))
				return
			}
			id := strings.TrimPrefix(p, "/admin/faults/")
			fa.faults[id] = b
			w.WriteHeader(http.StatusOK)
		case r.Method == "DELETE" && p == "/admin/faults":
			fa.faults = map[string]map[string]any{}
			w.WriteHeader(http.StatusOK)
		case r.Method == "DELETE" && strings.HasPrefix(p, "/admin/faults/"):
			delete(fa.faults, strings.TrimPrefix(p, "/admin/faults/"))
			w.WriteHeader(http.StatusOK) // idempotent
		case r.Method == "GET" && p == "/admin/state":
			var list []map[string]any
			for id, b := range fa.faults {
				list = append(list, map[string]any{"ID": id, "Kind": b["kind"]})
			}
			if list == nil {
				list = []map[string]any{}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"faults": list})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	return fa, s
}

func TestGatewayInjectorRoundTrip(t *testing.T) {
	_, s := newFakeAdmin()
	defer s.Close()
	g := NewGatewayInjector(s.URL, "tok", "kind-sre-lab")
	ctx := context.Background()
	in := Injection{ID: "f1", Kind: FaultDelay, Slot: "stable", DelayMs: 500, Fraction: 0.5, TTL: 75}
	if err := g.Apply(ctx, in); err != nil {
		t.Fatal(err)
	}
	live, err := g.Active(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(live) != 1 || live[0] != "f1" {
		t.Fatalf("live=%v", live)
	}
	if err := g.Clear(ctx, "missing"); err != nil {
		t.Fatalf("unknown clear must succeed: %v", err)
	}
	if err := g.Clear(ctx, "f1"); err != nil {
		t.Fatal(err)
	}
	if live, _ := g.Active(ctx); len(live) != 0 {
		t.Fatalf("live=%v after clear", live)
	}
}

func TestGatewayInjectorRejects(t *testing.T) {
	_, s := newFakeAdmin()
	defer s.Close()
	g := NewGatewayInjector(s.URL, "tok", "kind-sre-lab")
	ctx := context.Background()
	if err := g.Apply(ctx, Injection{ID: "f", Kind: "bogus", TTL: 10}); err == nil {
		t.Fatal("unknown kind accepted")
	}
	if err := g.Apply(ctx, Injection{ID: "f", Kind: FaultDelay, TTL: 999}); err == nil {
		t.Fatal("over-TTL accepted")
	}
	bad := NewGatewayInjector(s.URL, "wrong", "kind-sre-lab")
	if err := bad.Apply(ctx, Injection{ID: "f", Kind: FaultDelay, TTL: 10}); err == nil {
		t.Fatal("bad token accepted")
	}
}
