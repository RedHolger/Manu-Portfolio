// Package loadgen — open-loop scheduler + correctness history (§4.3).
//
// Planned start times are independent of response latency (open loop). A
// bounded worker pool records scheduled vs actual start; scheduling lag and
// missed launches are REPORTED, never silently absorbed. No default retries;
// the correctness profile retries ambiguous writes with the SAME key.
// Logical operation ID vs attempt ID: retries never inflate correctness.
package loadgen

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"math"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// Attempt is one JSONL line: a single HTTP try of a logical operation.
// Key/SKU/Qty are the synthetic request identity (load-<seed>-<n>, never
// customer data): the correctness oracle matches client-observed operations
// against the committed ledger by idempotency key, so the raw key travels
// with the attempt. Older histories simply omit them (omitempty).
type Attempt struct {
	OpID      string    `json:"op_id"`
	AttemptID int       `json:"attempt"`
	Key       string    `json:"key,omitempty"`
	SKU       string    `json:"sku,omitempty"`
	Qty       int64     `json:"qty,omitempty"`
	KeyHash   string    `json:"key_hash"`
	PlannedAt time.Time `json:"planned_at"`
	StartAt   time.Time `json:"start_at"`
	EndAt     time.Time `json:"end_at"`
	Slot      string    `json:"slot"`
	Code      int       `json:"code"`
	ErrClass  string    `json:"err_class"` // none|timeout|transport|http
	LatencyMs float64   `json:"latency_ms"`
	ResvID    string    `json:"reservation_id,omitempty"`
}

// Summary aggregates planned/offered/launched/completed/dropped/missed/
// outstanding plus the two phases of the run (schedule, then drain).
//
// Delivery contract — a workload is Valid only if ALL of these hold:
//  1. every planned slot is accounted for:
//     Planned == Offered + Missed == Launched + Dropped + Missed;
//  2. the schedule finished at or before the absolute deadline
//     (start + Duration): Missed == 0, i.e. Truncated == false;
//  3. drop rate (Dropped/Offered) ≤ 1%;
//  4. offered rate over the SCHEDULE window ≥ 90% of the nominal rate;
//  5. draining of already-launched work finished inside the bounded drain
//     window (Config.DrainTimeout, default 4×Timeout clamped to [5s,60s]);
//  6. the attempt history was written successfully.
//
// Scheduling duration and bounded draining are reported separately:
// ScheduleSeconds never absorbs drain time, and draining is never
// unbounded. Completed/Elapsed is reported for information only.
type Summary struct {
	// Planned is rate × duration — the slots the schedule promised.
	// Offered is what the scheduler attempted (Launched + Dropped).
	// Missed is planned slots never attempted because the absolute
	// deadline or a cancellation stopped the schedule.
	Planned, Offered, Launched, Completed, Dropped, Missed, Outstanding int
	// Cancelled is work abandoned when bounded draining hit DrainTimeout.
	Cancelled                    int
	SchedLagP50Ms, SchedLagP99Ms float64
	// OfferedRPS is delivery over the schedule window (the number that
	// must reach 90% of nominal). AchievedRPS is completions over the
	// whole run INCLUDING bounded drain — never a substitute for it.
	OfferedRPS, AchievedRPS                       float64
	ScheduleSeconds, DrainSeconds, ElapsedSeconds float64
	LatP50Ms, LatP95Ms, LatP99Ms                  float64
	TransportErrors                               int
	Successful                                    int    // 2xx completions (smoke gate)
	Valid                                         bool   // false if any delivery rule fails
	InvalidReason                                 string `json:",omitempty"`
	// Truncated reports that the schedule stopped before every planned
	// slot was attempted (deadline overrun or cancellation). Invariant:
	// Truncated == (Missed > 0).
	Truncated bool
	// DrainTimedOut reports that bounded draining exceeded DrainTimeout
	// and outstanding work was cancelled.
	DrainTimedOut bool
}

// Config for a run.
type Config struct {
	Rate      float64 // ops/sec
	Duration  time.Duration
	Seed      int64
	KeyPrefix string // isolates logical effects while keeping seeded fault draws comparable
	Timeout   time.Duration
	// CorrectnessProfile: retry 503/timeout/transport once with the same key.
	// All three classes are safe to re-offer: the idempotency key plus
	// request-hash compare makes effects at-most-once, so a retry can only
	// resolve to the original reservation, never duplicate it.
	CorrectnessProfile bool
	Workers            int
	// InvalidFraction of ops use an unknown SKU (→ 400 client_error).
	// Deterministic per (seed, n); excluded from SLIs, counted in load.
	InvalidFraction float64
	// DrainTimeout bounds draining after the schedule ends. Zero selects
	// 4 × Timeout clamped to [5s, 60s]. Exceeding it cancels outstanding
	// work and invalidates the run (never an unbounded wg.Wait).
	DrainTimeout time.Duration
}

// Runner executes against a gateway base URL.
type Runner struct {
	BaseURL string
	Client  *http.Client
	Out     io.Writer // JSONL attempts
	OnRetry func(opID string)
	// OnAttempt is called for every completed attempt, while loadgen's
	// internal lock is held. Callers must not call back into this package
	// from it; it exists so phase runners can collect oracle operations
	// without re-reading the JSONL history.
	OnAttempt func(a Attempt)

	// hookSlot runs before slot k is scheduled. Unexported and nil in
	// production: it exists so a regression test can stall the scheduler
	// and prove the absolute deadline (not a far stall guard) bounds the
	// schedule. Callers outside this package cannot set it.
	hookSlot func(k int)
}

// drainWindow bounds post-schedule draining (Config.DrainTimeout wins).
func drainWindow(cfg Config) time.Duration {
	if cfg.DrainTimeout > 0 {
		return cfg.DrainTimeout
	}
	d := 4 * cfg.Timeout
	if d < 5*time.Second {
		d = 5 * time.Second
	}
	if d > 60*time.Second {
		d = 60 * time.Second
	}
	return d
}

// waitTimeout returns true when wg finishes inside d.
func waitTimeout(wg *sync.WaitGroup, d time.Duration) bool {
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-done:
		return true
	case <-t.C:
		return false
	}
}

// Run executes the schedule; returns summary. Context cancels the run.
//
// The schedule is absolute: slot k is due at start + k×interval, where
// start is the monotonic time at entry and interval is 1/rate. Slots are
// dispatched in order, late slots are dispatched immediately with their
// ORIGINAL planned time (lag is measured against it), and no slot is ever
// attempted at or after the deadline start+Duration — the remainder is
// recorded as Missed. A time.Ticker cannot be used here: under load the
// runtime drops ticks, the loop then keeps firing until the tick COUNT is
// reached, and a configured 20s run silently stretched to 30s+ while still
// reporting Valid=true.
func (rn *Runner) Run(ctx context.Context, cfg Config) (Summary, error) {
	if math.IsNaN(cfg.Rate) || math.IsInf(cfg.Rate, 0) || cfg.Rate <= 0 || cfg.Rate > 1e6 || cfg.Duration <= 0 || cfg.Timeout <= 0 || math.IsNaN(cfg.InvalidFraction) || cfg.InvalidFraction < 0 || cfg.InvalidFraction > 1 {
		return Summary{}, fmt.Errorf("invalid load configuration")
	}
	if cfg.Workers <= 0 {
		cfg.Workers = 32
	}
	n := int(cfg.Rate * cfg.Duration.Seconds())
	interval := time.Duration(float64(time.Second) / cfg.Rate)
	jobs := make(chan job, cfg.Workers*2)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var attempts []Attempt
	var writeErr error
	var offered, launched, completed, dropped, transportErrs, successful int
	var lags, lats []float64

	// Workers run on a derived context so bounded draining can cancel
	// outstanding work instead of waiting unboundedly for it.
	rctx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()

	for w := 0; w < cfg.Workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				lag := time.Since(j.planned)
				mu.Lock()
				launched++
				lags = append(lags, float64(lag.Microseconds())/1000.0)
				mu.Unlock()
				att := rn.once(rctx, cfg, j)
				mu.Lock()
				completed++
				lats = append(lats, att.LatencyMs)
				if att.ErrClass == "transport" || att.ErrClass == "timeout" {
					transportErrs++
				}
				if att.Code >= 200 && att.Code < 300 {
					successful++
				}
				attempts = append(attempts, att)
				if rn.OnAttempt != nil {
					rn.OnAttempt(att)
				}
				mu.Unlock()
				if rn.Out != nil {
					raw, _ := json.Marshal(att)
					mu.Lock()
					if _, err := rn.Out.Write(append(raw, '\n')); err != nil && writeErr == nil {
						writeErr = err
					}
					mu.Unlock()
				}
			}
		}()
	}

	start := time.Now() // monotonic anchor for every planned slot
	deadline := start.Add(cfg.Duration)
	missed := 0
Loop:
	for k := 0; k < n; k++ {
		if rn.hookSlot != nil {
			rn.hookSlot(k)
		}
		planned := start.Add(time.Duration(k) * interval)
		if d := time.Until(planned); d > 0 {
			timer := time.NewTimer(d)
			select {
			case <-ctx.Done():
				timer.Stop()
				missed = n - k
				break Loop
			case <-timer.C:
			}
		}
		if !time.Now().Before(deadline) {
			// Absolute deadline: the rest of the schedule never happens.
			missed = n - k
			break Loop
		}
		opID := fmt.Sprintf("op-%d-%d", cfg.Seed, k+1)
		if cfg.KeyPrefix != "" {
			opID = cfg.KeyPrefix + "-" + opID
		}
		mu.Lock()
		offered++
		mu.Unlock()
		select {
		case jobs <- job{opID: opID, n: k + 1, planned: planned, seed: cfg.Seed}:
		default:
			mu.Lock()
			dropped++
			mu.Unlock()
		}
	}
	scheduleSecs := time.Since(start).Seconds()
	close(jobs)
	drained := waitTimeout(&wg, drainWindow(cfg))
	drainTimedOut := !drained
	outstandingAtCap := 0
	if drainTimedOut {
		mu.Lock()
		outstandingAtCap = launched - completed
		mu.Unlock()
		cancelRun() // bounded: abort what is still outstanding
		wg.Wait()
	}
	elapsed := time.Since(start)
	drainSecs := elapsed.Seconds() - scheduleSecs
	if drainSecs < 0 {
		drainSecs = 0
	}

	mu.Lock()
	defer mu.Unlock()
	sum := Summary{
		Planned: n, Offered: offered, Launched: launched,
		Completed: completed, Dropped: dropped, Missed: missed,
		Outstanding:     launched - completed,
		Cancelled:       outstandingAtCap,
		TransportErrors: transportErrs,
		Successful:      successful,
		ScheduleSeconds: scheduleSecs,
		DrainSeconds:    drainSecs,
		ElapsedSeconds:  elapsed.Seconds(),
		DrainTimedOut:   drainTimedOut,
		Truncated:       missed > 0,
	}
	sort.Float64s(lags)
	sort.Float64s(lats)
	sum.SchedLagP50Ms = pct(lags, 50)
	sum.SchedLagP99Ms = pct(lags, 99)
	sum.LatP50Ms = pct(lats, 50)
	sum.LatP95Ms = pct(lats, 95)
	sum.LatP99Ms = pct(lats, 99)
	if scheduleSecs > 0 {
		sum.OfferedRPS = float64(offered) / scheduleSecs
	}
	if elapsed.Seconds() > 0 {
		sum.AchievedRPS = float64(completed) / elapsed.Seconds()
	}
	sum.Valid = true
	switch {
	case n == 0:
		sum.Valid = false
		sum.InvalidReason = "no planned slots within duration"
	case missed > 0:
		sum.Valid = false
		sum.InvalidReason = fmt.Sprintf("%d/%d planned slots missed (schedule stopped at deadline)", missed, n)
	case offered != launched+dropped:
		sum.Valid = false
		sum.InvalidReason = fmt.Sprintf("accounting inconsistency: offered=%d launched=%d dropped=%d",
			offered, launched, dropped)
	case offered > 0 && float64(dropped)/float64(offered) > 0.01:
		sum.Valid = false
		sum.InvalidReason = fmt.Sprintf("drop rate %.2f%% > 1%%",
			100*float64(dropped)/float64(offered))
	case offered == 0:
		sum.Valid = false
		sum.InvalidReason = "no slots offered"
	case sum.OfferedRPS < 0.9*cfg.Rate:
		sum.Valid = false
		sum.InvalidReason = fmt.Sprintf("delivery rate %.1f rps < 90%% of nominal %.1f rps",
			sum.OfferedRPS, cfg.Rate)
	case drainTimedOut:
		sum.Valid = false
		sum.InvalidReason = fmt.Sprintf("drain exceeded %s with %d outstanding (cancelled)",
			drainWindow(cfg), outstandingAtCap)
	}
	if writeErr != nil {
		sum.Valid = false
		sum.InvalidReason = "history write failed"
		return sum, writeErr
	}
	return sum, nil
}

type job struct {
	opID    string
	n       int
	planned time.Time
	seed    int64
}

func (rn *Runner) once(ctx context.Context, cfg Config, j job) Attempt {
	key := fmt.Sprintf("load-%d-%d", cfg.Seed, j.n)
	if cfg.KeyPrefix != "" {
		key = cfg.KeyPrefix + "-" + key
	}
	att := Attempt{OpID: j.opID, AttemptID: 1, PlannedAt: j.planned,
		StartAt: time.Now(), Key: key, KeyHash: hashStr(key)}
	sku := "demo-item"
	if cfg.InvalidFraction > 0 {
		h := fnv.New32a()
		_, _ = h.Write([]byte(fmt.Sprintf("%d-%d", cfg.Seed, j.n)))
		if float64(h.Sum32()%1000)/1000.0 < cfg.InvalidFraction {
			sku = "no-such-sku" // → 400 client_error, SLI-excluded
		}
	}
	att.SKU, att.Qty = sku, 1
	code, slot, resv, eclass := rn.post(ctx, cfg, key, 1, sku, 1)
	// Correctness profile: ONE retry with the same key on any ambiguous or
	// safely-retryable outcome (timeout, transport error, HTTP 503).
	if cfg.CorrectnessProfile && (eclass == "timeout" || eclass == "transport" ||
		(eclass == "http" && code == 503)) {
		if rn.OnRetry != nil {
			rn.OnRetry(j.opID)
		}
		c2, s2, r2, e2 := rn.post(ctx, cfg, key, 2, sku, 1)
		att.AttemptID = 2
		code, slot, resv, eclass = c2, s2, r2, e2
	}
	att.EndAt = time.Now()
	att.LatencyMs = float64(att.EndAt.Sub(att.StartAt).Microseconds()) / 1000.0
	att.Code, att.Slot, att.ResvID, att.ErrClass = code, slot, resv, eclass
	return att
}

func (rn *Runner) post(ctx context.Context, cfg Config, key string, attempt int, sku string, qty int) (int, string, string, string) {
	body := fmt.Sprintf(`{"sku":%q,"quantity":%d}`, sku, qty)
	req, err := http.NewRequestWithContext(ctx, "POST", rn.BaseURL+"/v1/reservations",
		bytes.NewBufferString(body))
	if err != nil {
		return 0, "", "", "transport"
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", key)
	drawKey := key
	if cfg.KeyPrefix != "" {
		drawKey = strings.TrimPrefix(key, cfg.KeyPrefix+"-")
	}
	req.Header.Set("X-Operation-ID", fmt.Sprintf("%s-a%d", drawKey, attempt))
	req.Header.Set("X-Request-ID", fmt.Sprintf("%s-a%d", key, attempt))
	client := rn.Client
	if client == nil {
		client = &http.Client{Timeout: cfg.Timeout}
	}
	resp, err := client.Do(req)
	if err != nil {
		// A cancelled harness context is a timeout; a client-side timeout
		// (net.Error) is also a timeout even when the caller's context is
		// still alive — without this, timeouts misclassify as transport
		// and the correctness profile never retries them.
		if ctx.Err() != nil {
			return 0, "", "", "timeout"
		}
		var nerr net.Error
		if errors.As(err, &nerr) && nerr.Timeout() {
			return 0, "", "", "timeout"
		}
		return 0, "", "", "transport"
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	slot := resp.Header.Get("X-Slot")
	var parsed struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(raw, &parsed)
	if resp.StatusCode >= 400 {
		return resp.StatusCode, slot, "", "http"
	}
	return resp.StatusCode, slot, parsed.ID, "none"
}

func pct(sorted []float64, p int) float64 {
	if len(sorted) == 0 {
		return 0
	}
	i := (p * len(sorted)) / 100
	if i >= len(sorted) {
		i = len(sorted) - 1
	}
	return sorted[i]
}

func hashStr(s string) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(s))
	return fmt.Sprintf("%016x", h.Sum64())
}
