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
	"net"
	"net/http"
	"sort"
	"sync"
	"time"
)

// Attempt is one JSONL line: a single HTTP try of a logical operation.
type Attempt struct {
	OpID      string    `json:"op_id"`
	AttemptID int       `json:"attempt"`
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

// Summary aggregates offered/launched/completed/dropped/outstanding.
type Summary struct {
	Offered, Launched, Completed, Dropped, Outstanding int
	SchedLagP50Ms, SchedLagP99Ms                       float64
	AchievedRPS                                        float64
	LatP50Ms, LatP95Ms, LatP99Ms                       float64
	TransportErrors                                    int
	Successful                                         int    // 2xx completions (smoke gate)
	Valid                                              bool   // false if drop>1% or saturation
	InvalidReason                                      string `json:",omitempty"`
	// Truncated reports that the deadline ended the schedule before all n
	// planned ticks fired. Without this bound, dropped launches were
	// retried on later ticks and a configured duration silently extended
	// under overload (H5). Invariant: Offered == Launched + Dropped.
	Truncated bool
}

// Config for a run.
type Config struct {
	Rate     float64 // ops/sec
	Duration time.Duration
	Seed     int64
	Timeout  time.Duration
	// CorrectnessProfile: retry 503/timeout/transport once with the same key.
	// All three classes are safe to re-offer: the idempotency key plus
	// request-hash compare makes effects at-most-once, so a retry can only
	// resolve to the original reservation, never duplicate it.
	CorrectnessProfile bool
	Workers            int
	// InvalidFraction of ops use an unknown SKU (→ 400 client_error).
	// Deterministic per (seed, n); excluded from SLIs, counted in load.
	InvalidFraction float64
}

// Runner executes against a gateway base URL.
type Runner struct {
	BaseURL string
	Client  *http.Client
	Out     io.Writer // JSONL attempts
	OnRetry func(opID string)
}

// Run executes the schedule; returns summary. Context cancels the run.
func (rn *Runner) Run(ctx context.Context, cfg Config) (Summary, error) {
	if cfg.Workers <= 0 {
		cfg.Workers = 32
	}
	interval := time.Duration(float64(time.Second) / cfg.Rate)
	n := int(cfg.Rate * cfg.Duration.Seconds())
	jobs := make(chan job, cfg.Workers*2)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var attempts []Attempt
	var launched, completed, dropped, transportErrs, successful int
	var lags, lats []float64

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
				att := rn.once(ctx, cfg, j)
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
				mu.Unlock()
				if rn.Out != nil {
					raw, _ := json.Marshal(att)
					_, _ = rn.Out.Write(append(raw, '\n'))
				}
			}
		}()
	}
	start := time.Now()
	// The schedule ends by COUNT (every fired tick counts, enqueued or
	// dropped): drops are reported via Valid=false, never by stretching the
	// run. The deadline is only a stall guard (suspended clock, wedged
	// ticker) set far beyond any honest schedule.
	deadline := start.Add(cfg.Duration + 30*time.Second)
	ticked := 0
	tick := time.NewTicker(interval)
	defer tick.Stop()
Loop:
	for ticked < n {
		select {
		case <-ctx.Done():
			break Loop
		case planned := <-tick.C:
			if planned.After(deadline) {
				break Loop
			}
			ticked++
			opID := fmt.Sprintf("op-%d-%d", cfg.Seed, ticked)
			select {
			case jobs <- job{opID: opID, n: ticked, planned: planned, seed: cfg.Seed}:
			default:
				mu.Lock()
				dropped++
				mu.Unlock()
			}
		}
	}
	close(jobs)
	wg.Wait()
	el := time.Since(start)

	sum := Summary{
		Offered: ticked, Launched: launched,
		Completed: completed, Dropped: dropped,
		Outstanding: launched - completed, TransportErrors: transportErrs,
		Successful:  successful,
		AchievedRPS: float64(completed) / el.Seconds(),
		Truncated:   ticked < n,
	}
	sort.Float64s(lags)
	sort.Float64s(lats)
	sum.SchedLagP50Ms = pct(lags, 50)
	sum.SchedLagP99Ms = pct(lags, 99)
	sum.LatP50Ms = pct(lats, 50)
	sum.LatP95Ms = pct(lats, 95)
	sum.LatP99Ms = pct(lats, 99)
	sum.Valid = true
	if sum.Offered == 0 {
		sum.Valid = false
		sum.InvalidReason = "no ticks fired within duration"
	} else if float64(sum.Dropped)/float64(sum.Offered) > 0.01 {
		sum.Valid = false
		sum.InvalidReason = fmt.Sprintf("drop rate %.2f%% > 1%%",
			100*float64(sum.Dropped)/float64(sum.Offered))
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
	att := Attempt{OpID: j.opID, AttemptID: 1, PlannedAt: j.planned, StartAt: time.Now(), KeyHash: hashStr(key)}
	sku := "demo-item"
	if cfg.InvalidFraction > 0 {
		h := fnv.New32a()
		_, _ = h.Write([]byte(fmt.Sprintf("%d-%d", cfg.Seed, j.n)))
		if float64(h.Sum32()%1000)/1000.0 < cfg.InvalidFraction {
			sku = "no-such-sku" // → 400 client_error, SLI-excluded
		}
	}
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
	req.Header.Set("X-Operation-ID", fmt.Sprintf("%s-a%d", key, attempt))
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
