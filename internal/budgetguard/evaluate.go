// evaluate.go — release evaluation (§5.4): fetch → validate → decide → JSON.
// Exit codes: 0 PASS, 2 FAIL, 3 INCONCLUSIVE, 1 invalid/internal.
// Repeated evaluation of the same fixture+config is byte-identical except
// the explicitly excluded generated_at field.
package budgetguard

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"sre-portfolio/internal/telemetry"
)

// SlotCounts mirrors Counts with slot identity for reports.
type SlotCounts struct {
	Eligible int64 `json:"eligible"`
	Bad      int64 `json:"bad"`
	SlowBad  int64 `json:"slow_or_bad"`
}

// Result is the deterministic decision document.
type Result struct {
	SchemaVersion   int        `json:"schema_version"`
	Decision        string     `json:"decision"`
	Reasons         []string   `json:"reasons"`
	Window          Window     `json:"window"`
	Stable          SlotCounts `json:"stable"`
	Candidate       SlotCounts `json:"candidate"`
	HistoryComplete bool       `json:"history_complete"`
	PolicyHash      string     `json:"policy_hash"`
	Estimated       bool       `json:"estimated_counts"`
}

// Window uses RFC3339 UTC.
type Window struct {
	Start string `json:"start"`
	End   string `json:"end"`
}

// ConfigHash canonicalizes the config for policy_hash.
func ConfigHash(c ServiceSLO) string {
	raw, _ := json.Marshal(c)
	return fmt.Sprintf("%x", sha256.Sum256(raw))
}

// Decide runs the gate on counted slots (pure; used by the replay path).
// Fixture counts are integers by construction; estimated=false.
func Decide(cfg ServiceSLO, end time.Time, stable, cand SlotCounts) Result {
	return DecideCounts(cfg, end,
		Counts{Eligible: float64(stable.Eligible), Good: float64(stable.Eligible - stable.Bad),
			FastGood: float64(stable.Eligible - stable.SlowBad)},
		Counts{Eligible: float64(cand.Eligible), Good: float64(cand.Eligible - cand.Bad),
			FastGood: float64(cand.Eligible - cand.SlowBad)},
		stable, cand, false)
}

// DecideCounts runs the gate on RAW float counts (H6: no pre-rounding) and
// renders integer SlotCounts for display only.
func DecideCounts(cfg ServiceSLO, end time.Time, sc, cc Counts, stable, cand SlotCounts, estimated bool) Result {
	start := end.Add(-time.Duration(cfg.Gate.ObservationSeconds) * time.Second)
	decision, reasons := GateDecision(sc, cc, cfg.Gate)
	return Result{
		SchemaVersion: 1, Decision: decision, Reasons: reasons,
		Window: Window{Start: start.UTC().Format(time.RFC3339), End: end.UTC().Format(time.RFC3339)},
		Stable: stable, Candidate: cand,
		HistoryComplete: false, // lab never claims a real 30d window (§5.3)
		PolicyHash:      ConfigHash(cfg),
		Estimated:       estimated,
	}
}

// Fetched carries raw float counts for gating plus rounded display counts.
// The gate MUST use Raw (H6); Display exists only for the JSON report.
type Fetched struct {
	Stable, Candidate               Counts
	DisplayStable, DisplayCandidate SlotCounts
	Estimated                       bool
	Notes                           []string
}

// FetchCounts queries both slots over [end-observation, end] using
// sum(increase()) per series. Validates: full-window coverage per slot,
// freshness, both slots present, configured threshold bucket present.
// Any violation ⇒ error (caller maps to INCONCLUSIVE; never PASS).
func FetchCounts(ctx context.Context, cl *telemetry.Client, cfg ServiceSLO, end time.Time) (Fetched, error) {
	var out Fetched
	start := end.Add(-time.Duration(cfg.Gate.ObservationSeconds) * time.Second)
	svc := cfg.Service
	obs := fmt.Sprintf("%ds", cfg.Gate.ObservationSeconds)
	// Full-window coverage (H1): an 80s run must not report a 300s window.
	// Both slots checked before any counts are read.
	for _, slot := range []string{"stable", "candidate"} {
		if err := CheckCoverage(ctx, cl, svc, slot, start, end); err != nil {
			return out, fmt.Errorf("coverage: %w", err)
		}
	}
	// Freshness gate (§5.3): no recent scrape ⇒ INCONCLUSIVE, never PASS.
	freshQ := fmt.Sprintf("time() - max(timestamp(lab_requests_total{service=%q}))", svc)
	fresh, _, err := scalarSum(ctx, cl, freshQ, end)
	if err != nil {
		return out, fmt.Errorf("telemetry freshness: %w", err)
	}
	if fresh > float64(cfg.FreshnessSeconds) {
		return out, fmt.Errorf("stale telemetry: %.0fs old (limit %ds)", fresh, cfg.FreshnessSeconds)
	}
	for _, slot := range []string{"stable", "candidate"} {
		sel := fmt.Sprintf(`service=%q,slot=%q,route="/v1/reservations"`, svc, slot)
		// Eligible population excludes client_error (§5.3).
		eligQ := fmt.Sprintf("sum(increase(lab_requests_total{%s,result!=\"client_error\"}[%s]))", sel, obs)
		badQ := fmt.Sprintf("sum(increase(lab_requests_total{%s,result=~\"server_error|timeout|transport_error\"}[%s]))", sel, obs)
		elig, est1, err := scalarSum(ctx, cl, eligQ, end)
		if err != nil {
			return out, fmt.Errorf("slot %s eligible: %w", slot, err)
		}
		bad, est2, err := scalarSum(ctx, cl, badQ, end)
		if err != nil {
			return out, fmt.Errorf("slot %s bad: %w", slot, err)
		}
		// Slow-or-bad ≈ eligible − fast_good, where fast_good comes from the
		// cumulative threshold bucket (count/sum carry no le label).
		// The bucket MUST be the configured threshold (H6), not a literal.
		thr := strconv.FormatFloat(cfg.LatencyThreshold, 'g', -1, 64)
		fastQ := fmt.Sprintf(
			"sum(increase(lab_request_duration_seconds_bucket{%s,result=\"success\",le=%q}[%s]))",
			sel, thr, obs)
		fast, est3, err := scalarSum(ctx, cl, fastQ, end)
		if err != nil {
			return out, fmt.Errorf("slot %s fast: %w", slot, err)
		}
		raw := Counts{Eligible: elig, Good: elig - bad, FastGood: fast}
		disp := SlotCounts{Eligible: int64(elig + 0.5), Bad: int64(bad + 0.5),
			SlowBad: int64(elig - fast + 0.5)}
		if est1 || est2 || est3 {
			out.Notes = append(out.Notes, slot+":estimated")
			out.Estimated = true
		}
		if slot == "stable" {
			out.Stable, out.DisplayStable = raw, disp
		} else {
			out.Candidate, out.DisplayCandidate = raw, disp
		}
	}
	return out, nil
}

// scalarSum evaluates an instant query and sums vector values.
func scalarSum(ctx context.Context, cl *telemetry.Client, expr string, at time.Time) (float64, bool, error) {
	series, err := cl.Query(ctx, expr, at)
	if err != nil {
		return 0, false, err
	}
	if len(series) == 0 {
		return 0, false, fmt.Errorf("no data for %q", expr)
	}
	var vals []float64
	for _, s := range series {
		f, err := telemetry.SampleValue(s.Value[1])
		if err != nil {
			return 0, false, err
		}
		vals = append(vals, f)
	}
	total, estimated := telemetry.SumIncrease(vals)
	return total, estimated, nil
}

// ReplayFixture evaluates a recorded fixture (offline path — no Prometheus).
// Fixture schema: {"stable": {...SlotCounts}, "candidate": {...SlotCounts}}.
func ReplayFixture(cfg ServiceSLO, end time.Time, raw []byte) (Result, error) {
	var fx struct {
		Stable    SlotCounts `json:"stable"`
		Candidate SlotCounts `json:"candidate"`
	}
	if err := json.Unmarshal(raw, &fx); err != nil {
		return Result{}, err
	}
	return Decide(cfg, end, fx.Stable, fx.Candidate), nil
}
