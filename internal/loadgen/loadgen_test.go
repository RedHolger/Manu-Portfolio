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

// H5 regression: under saturation the run must end at the deadline — not
// extend until all n ticks enqueue — and report Truncated. Old code retried
// dropped launches on later ticks (≈60s wall for a 1s run here).
func TestSaturationTruncatesAtDeadline(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
		_ = r.Body.Close()
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"x"}`))
	}))
	defer s.Close()
	rn := &Runner{BaseURL: s.URL, Out: nil}
	start := time.Now()
	sum, err := rn.Run(context.Background(), Config{
		Rate: 200, Duration: time.Second, Seed: 3, Timeout: 5 * time.Second,
		Workers: 1,
	})
	el := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	if el > 15*time.Second {
		t.Fatalf("run took %v for a 1s duration (schedule extended)", el)
	}
	if sum.Truncated {
		t.Fatal("saturated run fired all ticks; drops belong in Valid=false, not Truncated")
	}
	if sum.Offered != 200 {
		t.Fatalf("offered=%d, want all 200 ticks", sum.Offered)
	}
	if sum.Valid {
		t.Fatal("98% drop rate must mark the run invalid")
	}
	if sum.Offered != sum.Launched+sum.Dropped {
		t.Fatalf("accounting inconsistent: offered=%d launched=%d dropped=%d",
			sum.Offered, sum.Launched, sum.Dropped)
	}
	if sum.Dropped == 0 {
		t.Fatal("expected drops under saturation")
	}
}

// Truncated fires only when the schedule itself is cut short (cancel).
func TestContextCancelTruncates(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.Body.Close()
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"x"}`))
	}))
	defer s.Close()
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(300 * time.Millisecond)
		cancel()
	}()
	rn := &Runner{BaseURL: s.URL, Out: nil}
	sum, err := rn.Run(ctx, Config{
		Rate: 50, Duration: 30 * time.Second, Seed: 5, Timeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !sum.Truncated {
		t.Fatal("cancelled schedule must report Truncated")
	}
	if sum.Offered >= 1500 {
		t.Fatalf("offered=%d, schedule should have stopped early", sum.Offered)
	}
}

// Accounting invariant on a clean run.
func TestAccountingConsistent(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.Body.Close()
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"x"}`))
	}))
	defer s.Close()
	rn := &Runner{BaseURL: s.URL, Out: nil}
	sum, err := rn.Run(context.Background(), Config{
		Rate: 50, Duration: time.Second, Seed: 4, Timeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if sum.Offered != 50 || sum.Offered != sum.Launched+sum.Dropped {
		t.Fatalf("offered=%d launched=%d dropped=%d", sum.Offered, sum.Launched, sum.Dropped)
	}
	if sum.Truncated {
		t.Fatal("clean run must not report Truncated")
	}
	if sum.Successful != sum.Completed {
		t.Fatalf("successful=%d completed=%d", sum.Successful, sum.Completed)
	}
}
