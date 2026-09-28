// B1 tests: parser goldens + SLI math edges + gate boundaries.
package budgetguard

import (
	"os"
	"testing"
)

const goodYAML = `apiVersion: portfolio.sre/v1
kind: ServiceSLO
metadata:
  name: reservations
spec:
  service: reservations
  period: 30d
  availability:
    target: 0.999
  latency:
    target: 0.99
    thresholdSeconds: 0.3
  excludeResults: [client_error]
  freshnessSeconds: 20
  releaseGate:
    observationSeconds: 300
    minRequestsPerSlot: 1000
    maxErrorRate: 0.01
    maxErrorRateIncrease: 0.005
    maxSlowRate: 0.02
    maxSlowRateIncrease: 0.01
`

func TestParseGolden(t *testing.T) {
	c, err := ParseConfig(goodYAML)
	if err != nil {
		t.Fatalf("golden: %v", err)
	}
	if c.Service != "reservations" || c.AvailabilityTarget != 0.999 ||
		c.LatencyTarget != 0.99 || c.LatencyThreshold != 0.3 ||
		len(c.ExcludeResults) != 1 || c.Gate.ObservationSeconds != 300 {
		t.Fatalf("golden mismatch: %+v", c)
	}
}

func TestParseRepoFixture(t *testing.T) {
	raw, err := os.ReadFile("../../configs/slos/reservations.yaml")
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}
	if _, err := ParseConfig(string(raw)); err != nil {
		t.Fatalf("repo fixture must parse: %v", err)
	}
}

func TestParseRejects(t *testing.T) {
	mk := func(edit string) string { return goodYAML + edit }
	_ = mk
	cases := map[string]string{
		"unknown field":      goodYAML + "  extraField: 1\n",
		"duplicate key":      goodYAML + "  period: 7d\n",
		"target zero":        replaceLine(goodYAML, "    target: 0.999", "    target: 0"),
		"target one":         replaceLine(goodYAML, "    target: 0.99", "    target: 1"),
		"bad bucket":         replaceLine(goodYAML, "    thresholdSeconds: 0.3", "    thresholdSeconds: 0.25"),
		"negative freshness": replaceLine(goodYAML, "  freshnessSeconds: 20", "  freshnessSeconds: -1"),
		"tab indent":         goodYAML + "\tbad: 1\n",
	}
	_ = cases
	for name, raw := range cases {
		if _, err := ParseConfig(raw); err == nil {
			t.Fatalf("%s: expected error, got nil", name)
		}
	}
}

// replaceLine swaps a full line (test helper).
func replaceLine(s, old, new string) string {
	out := ""
	for _, ln := range splitLines(s) {
		if ln == old {
			ln = new
		}
		out += ln + "\n"
	}
	return out
}

func splitLines(s string) []string {
	var r []string
	cur := ""
	for _, c := range s {
		if c == '\n' {
			r = append(r, cur)
			cur = ""
			continue
		}
		cur += string(c)
	}
	r = append(r, cur)
	return r
}

func TestZeroTrafficUnknown(t *testing.T) {
	r := Evaluate(Counts{}, 0.999, 0.99)
	if !r.Unknown || r.WhyUnknown == "" {
		t.Fatalf("N=0 must be UNKNOWN, got %+v", r)
	}
}

func TestNegativeBudgetPreserved(t *testing.T) {
	// 10% bad at 99.9% target: budget deeply negative, raw value kept.
	r := Evaluate(Counts{Eligible: 1000, Good: 900, FastGood: 900}, 0.999, 0.99)
	if r.Unknown {
		t.Fatal("should compute, not UNKNOWN")
	}
	if r.RemainingAvail >= 0 {
		t.Fatalf("RemainingAvail=%v, want negative", r.RemainingAvail)
	}
	if r.Availability != 0.9 {
		t.Fatalf("Availability=%v, want 0.9", r.Availability)
	}
}

func TestFailuresCountedOnce(t *testing.T) {
	// 10 failed of 1000: latency-bad must be exactly 10 (not 10 + re-added).
	r := Evaluate(Counts{Eligible: 1000, Good: 990, FastGood: 985}, 0.999, 0.99)
	if r.Unknown {
		t.Fatal("should compute")
	}
	want := float64(1000-985) / 1000
	if diff := r.BadRatioLat - want; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("BadRatioLat=%v, want %v", r.BadRatioLat, want)
	}
}

func TestGateBoundaries(t *testing.T) {
	g := ReleaseGate{MinRequestsPerSlot: 1000, MaxErrorRate: 0.01,
		MaxErrorRateIncrease: 0.005, MaxSlowRate: 0.02, MaxSlowRateIncrease: 0.01}
	mk := func(elig, bad, slowBad float64) Counts {
		return Counts{Eligible: elig, Good: elig - bad, FastGood: elig - slowBad}
	}
	// Exact boundary equality PASSES.
	d, _ := GateDecision(mk(10000, 100, 200), mk(1000, 10, 20), g)
	if d != "PASS" {
		t.Fatalf("boundary equality: got %s, want PASS", d)
	}
	// One over absolute error threshold FAILS.
	d, rs := GateDecision(mk(10000, 50, 100), mk(10000, 101, 150), g)
	if d != "FAIL" || !has(rs, "candidate_error_rate_exceeded") {
		t.Fatalf("got %s %v", d, rs)
	}
	// Under absolute but over increase FAILS (0.0051 > 0.005).
	d, rs = GateDecision(mk(10000, 20, 100), mk(10000, 71, 120), g)
	if d != "FAIL" || !has(rs, "candidate_error_rate_increase_exceeded") {
		t.Fatalf("got %s %v", d, rs)
	}
	// Unhealthy stable ⇒ INCONCLUSIVE (never blame candidate).
	d, rs = GateDecision(mk(10000, 500, 600), mk(10000, 500, 600), g)
	if d != "INCONCLUSIVE" || !has(rs, "baseline_unhealthy") {
		t.Fatalf("got %s %v", d, rs)
	}
	// Thin traffic ⇒ INCONCLUSIVE.
	d, rs = GateDecision(mk(100, 0, 0), mk(100, 0, 0), g)
	if d != "INCONCLUSIVE" || !has(rs, "insufficient_traffic") {
		t.Fatalf("got %s %v", d, rs)
	}
}

func has(ss []string, w string) bool {
	for _, s := range ss {
		if s == w {
			return true
		}
	}
	return false
}
