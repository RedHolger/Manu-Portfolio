package faultlab

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestFakeDepFaultTTL(t *testing.T) {
	f := NewFakeDepFault()
	ctx := context.Background()
	if bad, _ := f.IsFailing(ctx); bad {
		t.Fatal("starts disarmed")
	}
	if err := f.SetFail(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if bad, _ := f.IsFailing(ctx); !bad {
		t.Fatal("should be failing")
	}
	// Fake time travel: expiry in the past forces lazy clear.
	f.mu.Lock()
	f.expires = time.Now().Add(-time.Second)
	f.mu.Unlock()
	if bad, _ := f.IsFailing(ctx); bad {
		t.Fatal("TTL expiry must disarm")
	}
	if err := f.Clear(ctx); err != nil {
		t.Fatal(err)
	}
	if f.ClearCalls != 1 {
		t.Fatalf("clears=%d", f.ClearCalls)
	}
}

func TestHTTPDepFaultRoundTrip(t *testing.T) {
	var failing bool
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.Method == "PUT" {
			var b map[string]any
			_ = json.NewDecoder(r.Body).Decode(&b)
			failing, _ = b["fail"].(bool)
			w.WriteHeader(http.StatusOK)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]bool{"failing": failing})
	}))
	defer s.Close()
	h := NewHTTPDepFault(s.URL, "tok")
	ctx := context.Background()
	if err := h.SetFail(ctx, 60); err != nil {
		t.Fatal(err)
	}
	bad, err := h.IsFailing(ctx)
	if err != nil || !bad {
		t.Fatalf("bad=%v err=%v", bad, err)
	}
	if err := h.Clear(ctx); err != nil {
		t.Fatal(err)
	}
	if bad, _ := h.IsFailing(ctx); bad {
		t.Fatal("should be clear")
	}
	if err := NewHTTPDepFault(s.URL, "wrong").Clear(ctx); err == nil {
		t.Fatal("bad token accepted")
	}
}
