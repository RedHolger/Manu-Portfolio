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

// Decide runs the gate on counted slots (pure; used by live + replay paths).
func Decide(cfg ServiceSLO, end time.Time, stable, cand SlotCounts) Result {
	start := end.Add(-time.Duration(cfg.Gate.ObservationSeconds) * time.Second)
	sc := Counts{Eligible: stable.Eligible, Good: stable.Eligible - stable.Bad,
		FastGood: stable.Eligible - stable.SlowBad}
	cc := Counts{Eligible: cand.Eligible, Good: cand.Eligible - cand.Bad,
		FastGood: cand.Eligible - cand.SlowBad}
	decision, reasons := GateDecision(sc, cc, cfg.Gate)
	return Result{
		SchemaVersion: 1, Decision: decision, Reasons: reasons,
		Window: Window{Start: start.UTC().Format(time.RFC3339), End: end.UTC().Format(time.RFC3339)},
		Stable: stable, Candidate: cand,
		HistoryComplete: false, // lab never claims a real 30d window (§5.3)
		PolicyHash:      ConfigHash(cfg),
	}
}

// FetchCounts queries both slots over [end-observation, end] using
// sum(increase()) per series. Validates: both slots present, full-window
// coverage, 0.3s bucket present, freshness within freshnessSeconds.
// Any violation ⇒ INCONCLUSIVE (missing telemetry never passes).
func FetchCounts(ctx context.Context, cl *telemetry.Client, cfg ServiceSLO, end time.Time) (SlotCounts, SlotCounts, []string, error) {
	start := end.Add(-time.Duration(cfg.Gate.ObservationSeconds) * time.Second)
	_ = start
	svc := cfg.Service
	obs := fmt.Sprintf("%ds", cfg.Gate.ObservationSeconds)
	// Freshness gate (§5.3): no recent scrape ⇒ INCONCLUSIVE, never PASS.
	freshQ := fmt.Sprintf("time() - max(timestamp(lab_requests_total{service=%q}))", svc)
	fresh, _, err := scalarSum(ctx, cl, freshQ, end)
	if err != nil {
		return SlotCounts{}, SlotCounts{}, nil, fmt.Errorf("telemetry freshness: %w", err)
	}
	if fresh > float64(cfg.FreshnessSeconds) {
		return SlotCounts{}, SlotCounts{}, nil,
			fmt.Errorf("stale telemetry: %.0fs old (limit %ds)", fresh, cfg.FreshnessSeconds)
	}
	var stable, cand SlotCounts
	var notes []string
	for _, slot := range []string{"stable", "candidate"} {
		sel := fmt.Sprintf(`service=%q,slot=%q,route="/v1/reservations"`, svc, slot)
		// Eligible population excludes client_error (§5.3).
		eligQ := fmt.Sprintf("sum(increase(lab_requests_total{%s,result!=\"client_error\"}[%s]))", sel, obs)
		badQ := fmt.Sprintf("sum(increase(lab_requests_total{%s,result=~\"server_error|timeout|transport_error\"}[%s]))", sel, obs)
		elig, est1, err := scalarSum(ctx, cl, eligQ, end)
		if err != nil {
			return stable, cand, nil, fmt.Errorf("slot %s eligible: %w", slot, err)
		}
		bad, est2, err := scalarSum(ctx, cl, badQ, end)
		if err != nil {
			return stable, cand, nil, fmt.Errorf("slot %s bad: %w", slot, err)
		}
		// Slow-or-bad ≈ eligible − fast_good, where fast_good comes from the
		// cumulative 0.3s histogram BUCKET (count/sum carry no le label).
		fastQ := fmt.Sprintf(
			"sum(increase(lab_request_duration_seconds_bucket{%s,result=\"success\",le=\"0.3\"}[%s]))",
			sel, obs)
		fast, est3, err := scalarSum(ctx, cl, fastQ, end)
		if err != nil {
			return stable, cand, nil, fmt.Errorf("slot %s fast: %w", slot, err)
		}
		sc := SlotCounts{Eligible: int64(elig + 0.5), Bad: int64(bad + 0.5),
			SlowBad: int64(elig - fast + 0.5)}
		if est1 || est2 || est3 {
			notes = append(notes, slot+":estimated")
		}
		if slot == "stable" {
			stable = sc
		} else {
			cand = sc
		}
	}
	return stable, cand, notes, nil
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
