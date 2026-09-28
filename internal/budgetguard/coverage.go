// coverage.go — full-window coverage gate (§5.3, H1).
//
// FetchCounts previously used only the newest service-wide timestamp and
// never verified the observation window was actually observed: an 80s run
// reported a 300s window. Now both slots must show ≤10% missing samples and
// no gap >20s across [start, end], or evaluation is INCONCLUSIVE.
package budgetguard

import (
	"context"
	"fmt"
	"time"

	"sre-portfolio/internal/telemetry"
)

// Coverage parameters (spec §5.3: ≤10% missing, no gap >20s).
const (
	coverageStep       = 15 * time.Second
	coverageMaxGap     = 20 * time.Second
	coverageMaxMissing = 0.10
)

// CheckCoverage validates one slot's series presence over the full window.
func CheckCoverage(ctx context.Context, cl *telemetry.Client, service, slot string, start, end time.Time) error {
	expr := fmt.Sprintf("sum(lab_requests_total{service=%q,slot=%q})", service, slot)
	series, err := cl.QueryMatrix(ctx, expr, start, end, coverageStep)
	if err != nil {
		return fmt.Errorf("slot %s coverage query: %w", slot, err)
	}
	var stamps []float64
	for _, s := range series {
		for _, v := range s.Values {
			ts, err := telemetry.SampleValue(v[0])
			if err != nil {
				return fmt.Errorf("slot %s bad timestamp: %w", slot, err)
			}
			stamps = append(stamps, ts)
		}
	}
	if len(stamps) == 0 {
		return fmt.Errorf("slot %s: no samples in window", slot)
	}
	expected := int(end.Sub(start)/coverageStep) + 1
	missing := expected - len(stamps)
	if missing < 0 {
		missing = 0
	}
	if float64(missing)/float64(expected) > coverageMaxMissing {
		return fmt.Errorf("slot %s: %d/%d samples missing (>%.0f%%)",
			slot, missing, expected, coverageMaxMissing*100)
	}
	prev := stamps[0]
	for _, ts := range stamps[1:] {
		if gap := ts - prev; gap > coverageMaxGap.Seconds() {
			return fmt.Errorf("slot %s: %.0fs sample gap (>%.0fs)",
				slot, gap, coverageMaxGap.Seconds())
		}
		prev = ts
	}
	// Edges: first sample must fall within one step of window start, last
	// within one step of window end — else the window is not fully observed.
	startUnix, endUnix := float64(start.Unix()), float64(end.Unix())
	if stamps[0]-startUnix > coverageStep.Seconds() {
		return fmt.Errorf("slot %s: first sample %.0fs after window start", slot, stamps[0]-startUnix)
	}
	if endUnix-stamps[len(stamps)-1] > coverageStep.Seconds() {
		return fmt.Errorf("slot %s: last sample %.0fs before window end", slot, endUnix-stamps[len(stamps)-1])
	}
	return nil
}
