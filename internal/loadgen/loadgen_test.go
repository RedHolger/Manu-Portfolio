// Invalid-fraction traffic: deterministic unknown-SKU ops → client 400s,
// recorded in history but SLI-excluded downstream.
package loadgen

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
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

// Correctness profile retries once, same key, on timeout/transport/503.
// Client-side timeouts must classify as "timeout" even though the harness
// context is still alive (regression: they misclassified as transport and
// the retry never fired live).
func TestRetryOnceSameKey(t *testing.T) {
	mkJob := func() job {
		return job{opID: "op-1-1", n: 1, planned: time.Now(), seed: 1}
	}
	t.Run("timeout", func(t *testing.T) {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(300 * time.Millisecond) // longer than client timeout
			w.WriteHeader(http.StatusCreated)
		}))
		defer s.Close()
		rn := &Runner{BaseURL: s.URL}
		att := rn.once(context.Background(), Config{Timeout: 50 * time.Millisecond, CorrectnessProfile: true}, mkJob())
		if att.AttemptID != 2 {
			t.Fatalf("attempt=%d, want 2 (timeout retried)", att.AttemptID)
		}
		if att.ErrClass != "timeout" {
			t.Fatalf("errclass=%q, want timeout", att.ErrClass)
		}
	})
	t.Run("transport", func(t *testing.T) {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
		url := s.URL
		s.Close() // refused connection
		rn := &Runner{BaseURL: url}
		att := rn.once(context.Background(), Config{Timeout: 2 * time.Second, CorrectnessProfile: true}, mkJob())
		if att.AttemptID != 2 {
			t.Fatalf("attempt=%d, want 2 (transport retried)", att.AttemptID)
		}
		if att.ErrClass != "transport" {
			t.Fatalf("errclass=%q, want transport", att.ErrClass)
		}
	})
	t.Run("flaky503", func(t *testing.T) {
		var n atomic.Int64
		var keys []string
		var mu sync.Mutex
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			keys = append(keys, r.Header.Get("Idempotency-Key"))
			mu.Unlock()
			if n.Add(1) == 1 {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":"same"}`))
		}))
		defer s.Close()
		rn := &Runner{BaseURL: s.URL}
		att := rn.once(context.Background(), Config{Timeout: 2 * time.Second, CorrectnessProfile: true}, mkJob())
		if att.AttemptID != 2 || att.Code != http.StatusCreated || att.ResvID != "same" {
			t.Fatalf("got attempt=%d code=%d id=%q", att.AttemptID, att.Code, att.ResvID)
		}
		if len(keys) != 2 || keys[0] != keys[1] {
			t.Fatalf("retry must reuse the key, got %v", keys)
		}
	})
	t.Run("no-retry-without-profile", func(t *testing.T) {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
		}))
		defer s.Close()
		rn := &Runner{BaseURL: s.URL}
		att := rn.once(context.Background(), Config{Timeout: 2 * time.Second}, mkJob())
		if att.AttemptID != 1 || att.Code != http.StatusServiceUnavailable {
			t.Fatalf("got attempt=%d code=%d", att.AttemptID, att.Code)
		}
	})
	t.Run("no-retry-on-success-or-400", func(t *testing.T) {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
		}))
		defer s.Close()
		rn := &Runner{BaseURL: s.URL}
		att := rn.once(context.Background(), Config{Timeout: 2 * time.Second, CorrectnessProfile: true}, mkJob())
		if att.AttemptID != 1 || att.Code != http.StatusBadRequest {
			t.Fatalf("got attempt=%d code=%d", att.AttemptID, att.Code)
		}
	})
}

// Different experimental arms must produce new writes but identical seeded draws.
func TestKeyPrefixSeparatesEffectsPreservesDraw(t *testing.T) {
	var keys, draws []string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		keys = append(keys, r.Header.Get("Idempotency-Key"))
		draws = append(draws, r.Header.Get("X-Operation-ID"))
		w.WriteHeader(201)
	}))
	defer s.Close()
	rn := &Runner{BaseURL: s.URL}
	for _, prefix := range []string{"arm-a", "arm-b"} {
		rn.once(context.Background(), Config{KeyPrefix: prefix, Timeout: time.Second}, job{opID: "op-5-1", n: 1, seed: 5, planned: time.Now()})
	}
	if keys[0] == keys[1] || draws[0] != draws[1] {
		t.Fatalf("keys=%v draws=%v", keys, draws)
	}
}

type failedWriter struct{}

func (failedWriter) Write(p []byte) (int, error) { return 0, fmt.Errorf("disk full") }
func TestEvidenceWriteFailureReturned(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(201) }))
	defer s.Close()
	rn := &Runner{BaseURL: s.URL, Out: failedWriter{}}
	_, err := rn.Run(context.Background(), Config{Rate: 20, Duration: 100 * time.Millisecond, Timeout: time.Second})
	if err == nil {
		t.Fatal("evidence write failure hidden")
	}
}

// A-1 regression: the schedule is bounded by the ABSOLUTE deadline
// (start + duration), never by a far stall guard. A deliberate scheduler
// stall must mark the remaining slots Missed and invalidate the run —
// the old ticker loop kept firing until it received n ticks, so a stalled
// scheduler stretched a configured duration and still reported Valid=true.
func TestStalledScheduleStopsAtAbsoluteDeadline(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.Body.Close()
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"x"}`))
	}))
	defer s.Close()
	rn := &Runner{BaseURL: s.URL}
	stalled := false
	rn.hookSlot = func(k int) {
		if k == 1 && !stalled {
			stalled = true
			time.Sleep(600 * time.Millisecond) // past the 500ms deadline
		}
	}
	start := time.Now()
	sum, err := rn.Run(context.Background(), Config{
		Rate: 100, Duration: 500 * time.Millisecond, Seed: 1, Timeout: 2 * time.Second,
	})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Planned != 50 {
		t.Fatalf("planned=%d, want 50 (rate x duration)", sum.Planned)
	}
	if sum.Missed == 0 {
		t.Fatalf("missed=0, want the slots after the stall recorded as missed (offered=%d)", sum.Offered)
	}
	if sum.Offered+sum.Missed != sum.Planned {
		t.Fatalf("accounting: offered=%d missed=%d planned=%d",
			sum.Offered, sum.Missed, sum.Planned)
	}
	if !sum.Truncated {
		t.Fatal("deadline overrun must report Truncated")
	}
	if sum.Valid {
		t.Fatalf("stalled schedule must be invalid, reason=%q", sum.InvalidReason)
	}
	if !strings.Contains(sum.InvalidReason, "missed") {
		t.Fatalf("invalid reason should name missed slots, got %q", sum.InvalidReason)
	}
	// Bounded: the old duration+30s stall guard would allow ~30.5s here.
	if elapsed > 5*time.Second {
		t.Fatalf("stalled schedule took %v for a 500ms run", elapsed)
	}
}

// A-2 regression: scheduling duration and bounded draining are reported
// separately, and delivery is measured over the SCHEDULE window.
func TestScheduleAndDrainReportedSeparately(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.Body.Close()
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"x"}`))
	}))
	defer s.Close()
	rn := &Runner{BaseURL: s.URL}
	sum, err := rn.Run(context.Background(), Config{
		Rate: 50, Duration: time.Second, Seed: 7, Timeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if sum.Missed != 0 || sum.Truncated {
		t.Fatalf("clean run: missed=%d truncated=%v", sum.Missed, sum.Truncated)
	}
	if sum.Planned != 50 || sum.Offered != 50 {
		t.Fatalf("planned=%d offered=%d, want 50/50", sum.Planned, sum.Offered)
	}
	if sum.ScheduleSeconds < 0.8 || sum.ScheduleSeconds > 2.5 {
		t.Fatalf("schedule seconds %.3f, want ≈1s (duration must not absorb drain)", sum.ScheduleSeconds)
	}
	if sum.ElapsedSeconds < sum.ScheduleSeconds {
		t.Fatalf("elapsed %.3f < schedule %.3f", sum.ElapsedSeconds, sum.ScheduleSeconds)
	}
	if sum.OfferedRPS < 45 || sum.OfferedRPS > 60 {
		t.Fatalf("offered rps %.1f, want ≈50", sum.OfferedRPS)
	}
	if sum.AchievedRPS <= 0 {
		t.Fatal("achieved rps must be reported")
	}
	if sum.Cancelled != 0 || sum.DrainTimedOut {
		t.Fatalf("clean run must not cancel work: cancelled=%d drainTimedOut=%v",
			sum.Cancelled, sum.DrainTimedOut)
	}
	if !sum.Valid {
		t.Fatalf("clean run invalid: %q", sum.InvalidReason)
	}
}

// A-3 regression: draining is bounded. Work still in flight when the drain
// window closes is cancelled and invalidates the run instead of waiting
// unboundedly.
func TestBoundedDrainCancelsAndInvalidates(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(600 * time.Millisecond)
		_ = r.Body.Close()
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"x"}`))
	}))
	defer s.Close()
	rn := &Runner{BaseURL: s.URL}
	start := time.Now()
	sum, err := rn.Run(context.Background(), Config{
		Rate: 4, Duration: time.Second, Seed: 2, Timeout: 5 * time.Second,
		Workers: 2, DrainTimeout: 300 * time.Millisecond,
	})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	if !sum.DrainTimedOut || sum.Cancelled == 0 {
		t.Fatalf("want cancelled work at the drain cap: drainTimedOut=%v cancelled=%d",
			sum.DrainTimedOut, sum.Cancelled)
	}
	if sum.Valid {
		t.Fatalf("drain overrun must invalidate the run: %q", sum.InvalidReason)
	}
	if !strings.Contains(sum.InvalidReason, "drain exceeded") {
		t.Fatalf("reason=%q", sum.InvalidReason)
	}
	if elapsed > 4*time.Second {
		t.Fatalf("bounded drain took %v", elapsed)
	}
}
