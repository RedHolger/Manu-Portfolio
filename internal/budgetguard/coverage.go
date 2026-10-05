// coverage.go — full-window coverage gate (§5.3, H1, finding B).
//
// Coverage counts SOURCE samples, never query-range evaluation points.
// Prometheus query_range replays the last value across a scrape gap (5m
// lookback), so real 60-second sample spacing used to produce a full
// 15-second grid of points and PASSed. The gate now evaluates
// count_over_time() — a function over raw TSDB samples — once per bucket,
// so a bucket with no real scrape is a missing bucket. Repeated lookback
// values are never counted as new scrapes.
//
// Both required series (the request counter used for eligible/bad counts
// and the duration bucket used for slow-or-bad) must show:
//   - ≤10% of buckets without a source sample,
//   - no run of empty buckets longer than 20s,
//   - the first and last bucket of the window populated (edges).
//
// Any violation ⇒ error ⇒ INCONCLUSIVE (never PASS).
package budgetguard

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"time"

	"sre-portfolio/internal/telemetry"
)

// Coverage parameters (spec §5.3: ≤10% missing, no gap >20s).
const (
	coverageStep       = 15 * time.Second
	coverageMaxGap     = 20 * time.Second
	coverageMaxMissing = 0.10
)

// coverageExprs are the source series the gate requires over the window:
// the request counter behind eligible/bad and the CONFIGURED latency bucket
// behind slow-or-bad. No result-class filter on purpose — a slot with no
// successes must stay measurable (absence of successes is FAIL or
// INCONCLUSIVE, never "no coverage").
func coverageExprs(service, slot, threshold string) []string {
	return []string{
		fmt.Sprintf("sum(count_over_time(lab_requests_total{service=%q,slot=%q}[%s]))",
			service, slot, coverageStep),
		fmt.Sprintf("sum(count_over_time(lab_request_duration_seconds_bucket{service=%q,slot=%q,le=%q}[%s]))",
			service, slot, threshold, coverageStep),
	}
}

// CheckCoverage validates one slot's source-sample coverage over [start,end]
// for the exact series the gate reads (cfg.LatencyThreshold bucket included).
func CheckCoverage(ctx context.Context, cl *telemetry.Client, cfg ServiceSLO, slot string, start, end time.Time) error {
	service := cfg.Service
	threshold := strconv.FormatFloat(cfg.LatencyThreshold, 'g', -1, 64)
	window := end.Sub(start)
	if window <= 0 {
		return fmt.Errorf("slot %s: empty observation window", slot)
	}
	expected := int(window / coverageStep)
	if expected < 1 {
		return fmt.Errorf("slot %s: window shorter than one coverage step", slot)
	}
	startF := float64(start.UnixNano()) / 1e9
	stepS := coverageStep.Seconds()
	for _, expr := range coverageExprs(service, slot, threshold) {
		// Bucket i covers (start+(i-1)·step, start+i·step]: evaluate from
		// start+step so the first bucket never reaches before the window.
		series, err := cl.QueryMatrix(ctx, expr, start.Add(coverageStep), end, coverageStep)
		if err != nil {
			return fmt.Errorf("slot %s coverage query: %w", slot, err)
		}
		present := make([]bool, expected+1)
		for _, s := range series {
			for _, v := range s.Values {
				ts, err := telemetry.SampleValue(v[0])
				if err != nil {
					return fmt.Errorf("slot %s bad timestamp: %w", slot, err)
				}
				val, err := telemetry.SampleValue(v[1])
				if err != nil {
					return fmt.Errorf("slot %s bad coverage sample: %w", slot, err)
				}
				if !(val > 0) {
					continue // a zero/absent count is not a covered bucket
				}
				idx := int(math.Round((ts - startF) / stepS))
				if idx < 1 || idx > expected {
					continue // outside the window: not evidence for it
				}
				present[idx] = true
			}
		}
		missing, worst, run := 0, 0, 0
		for i := 1; i <= expected; i++ {
			if present[i] {
				run = 0
				continue
			}
			missing++
			run++
			if run > worst {
				worst = run
			}
		}
		if missing == expected {
			return fmt.Errorf("slot %s: no source samples in window", slot)
		}
		if float64(missing)/float64(expected) > coverageMaxMissing {
			return fmt.Errorf("slot %s: %d/%d buckets without source samples (>%.0f%%)",
				slot, missing, expected, coverageMaxMissing*100)
		}
		if gap := time.Duration(worst) * coverageStep; gap > coverageMaxGap {
			return fmt.Errorf("slot %s: %.0fs without source samples (>%.0fs)",
				slot, gap.Seconds(), coverageMaxGap.Seconds())
		}
		// Edges: the buckets touching the window boundaries must hold real
		// samples — else the window is not fully observed.
		if !present[1] {
			return fmt.Errorf("slot %s: no source sample within one step of window start", slot)
		}
		if !present[expected] {
			return fmt.Errorf("slot %s: no source sample within one step of window end", slot)
		}
	}
	return nil
}
