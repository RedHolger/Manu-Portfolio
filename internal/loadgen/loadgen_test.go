// Invalid-fraction traffic: deterministic unknown-SKU ops → client 400s,
// recorded in history but SLI-excluded downstream.
package loadgen

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestInvalidFraction(t *testing.T) {
	var ok, bad atomic.Int64
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			SKU string `json:"sku"`
		}
		_ = json.NewDecoder(r.Body).Decode(&b)
		_ = r.Body.Close()
		w.Header().Set("Content-Type", "application/json")
		if b.SKU == "no-such-sku" {
			bad.Add(1)
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"unknown sku"}`))
			return
		}
		ok.Add(1)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"x"}`))
	}))
	defer s.Close()
	rn := &Runner{BaseURL: s.URL, Out: nil}
	sum, err := rn.Run(context.Background(), Config{
		Rate: 50, Duration: 2 * time.Second, Seed: 9, Timeout: 2 * time.Second,
		InvalidFraction: 0.5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if sum.Offered != 100 || sum.Completed != 100 || !sum.Valid {
		t.Fatalf("offered=%d completed=%d valid=%v", sum.Offered, sum.Completed, sum.Valid)
	}
	if bad.Load() == 0 || ok.Load() == 0 {
		t.Fatalf("want a mix of valid/invalid on the wire, got ok=%d bad=%d", ok.Load(), bad.Load())
	}
	if frac := float64(bad.Load()) / 100; frac < 0.3 || frac > 0.7 {
		t.Fatalf("invalid fraction=%v, want ~0.5", frac)
	}
}

func TestNoInvalidByDefault(t *testing.T) {
	var bad atomic.Int64
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			SKU string `json:"sku"`
		}
		_ = json.NewDecoder(r.Body).Decode(&b)
		_ = r.Body.Close()
		if b.SKU == "no-such-sku" {
			bad.Add(1)
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"x"}`))
	}))
	defer s.Close()
	rn := &Runner{BaseURL: s.URL, Out: nil}
	_, err := rn.Run(context.Background(), Config{
		Rate: 50, Duration: time.Second, Seed: 9, Timeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if bad.Load() != 0 {
		t.Fatalf("default must send zero invalid ops, got %d", bad.Load())
	}
}
