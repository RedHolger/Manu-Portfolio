// sli.go — SLI math (§5.3). Eligible population excludes client_error.
// All upstream errors, timeouts, transport errors are eligible BAD requests.
//
// N        = eligible count
// G_avail  = successful eligible count
// G_lat    = successful eligible completed within threshold
// availability = G_avail / N ; latencySLI = G_lat / N
// bad_ratio = 1 - good/N ; burn = bad_ratio / (1-target)
// allowed_bad = N*(1-target) ; remaining = 1 - bad/allowed_bad (may be <0)
//
// Counts are float64 because Prometheus increase() extrapolates fractional
// values. Gate comparisons use the RAW fractions: rounding before comparing
// can flip boundary decisions (H6). Integer display rounding happens only
// at the JSON report edge, flagged via Result.Estimated.
package budgetguard

import (
	"math"
)

// Counts is one slot's observed population over a window.
type Counts struct {
	Eligible float64 // N
	Good     float64 // successful eligible (availability good)
	FastGood float64 // successful eligible within threshold (latency good)
}

// SLIResult carries values or UNKNOWN.
type SLIResult struct {
	Availability, Latency float64
	BadRatioAvail         float64
	BadRatioLat           float64
	BurnAvail             float64
	BurnLat               float64
	AllowedBadAvail       float64
	AllowedBadLat         float64
	RemainingAvail        float64 // may be negative; raw preserved
	RemainingLat          float64
	Unknown               bool
	WhyUnknown            string
}

// Evaluate computes both SLIs. N<=0, negative inputs, or nonfinite values
// yield UNKNOWN — never 100% (no-traffic is not perfect availability).
func Evaluate(c Counts, availTarget, latTarget float64) SLIResult {
	if c.Eligible <= 0 {
		return SLIResult{Unknown: true, WhyUnknown: "no eligible traffic in window"}
	}
	if c.Good < 0 || c.FastGood < 0 || c.Good > c.Eligible || c.FastGood > c.Good {
		return SLIResult{Unknown: true, WhyUnknown: "inconsistent counts (good<=eligible, fast<=good required)"}
	}
	r := SLIResult{
		Availability:    c.Good / c.Eligible,
		Latency:         c.FastGood / c.Eligible,
		AllowedBadAvail: c.Eligible * (1 - availTarget),
		AllowedBadLat:   c.Eligible * (1 - latTarget),
	}
	r.BadRatioAvail = 1 - r.Availability
	r.BadRatioLat = 1 - r.Latency
	r.BurnAvail = r.BadRatioAvail / (1 - availTarget)
	r.BurnLat = r.BadRatioLat / (1 - latTarget)
	badAvail := c.Eligible - c.Good
	badLat := c.Eligible - c.FastGood
	// Latency is joint success-and-speed: a failure counts as bad once
	// (FastGood already excludes failures — never add them again).
	r.RemainingAvail = 1 - badAvail/r.AllowedBadAvail
	r.RemainingLat = 1 - badLat/r.AllowedBadLat
	for _, v := range []float64{r.Availability, r.Latency, r.BurnAvail, r.BurnLat} {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return SLIResult{Unknown: true, WhyUnknown: "nonfinite SLI value"}
		}
	}
	return r
}

// GateDecision applies the §5.4 demo heuristics. Boundary equality PASSES.
// Returns (decision, reasons) with decision ∈ PASS|FAIL|INCONCLUSIVE.
func GateDecision(stable, cand Counts, g ReleaseGate) (string, []string) {
	var reasons []string
	add := func(s string) { reasons = append(reasons, s) }
	if stable.Eligible < float64(g.MinRequestsPerSlot) || cand.Eligible < float64(g.MinRequestsPerSlot) {
		add("insufficient_traffic")
		return "INCONCLUSIVE", reasons
	}
	serr := (stable.Eligible - stable.Good) / stable.Eligible
	serrSlow := (stable.Eligible - stable.FastGood) / stable.Eligible
	if serr > g.MaxErrorRate || serrSlow > g.MaxSlowRate {
		add("baseline_unhealthy")
		return "INCONCLUSIVE", reasons // never blame candidate for shared failure
	}
	cerr := (cand.Eligible - cand.Good) / cand.Eligible
	cerrSlow := (cand.Eligible - cand.FastGood) / cand.Eligible
	fail := false
	if cerr > g.MaxErrorRate {
		add("candidate_error_rate_exceeded")
		fail = true
	}
	if cerr-serr > g.MaxErrorRateIncrease {
		add("candidate_error_rate_increase_exceeded")
		fail = true
	}
	if cerrSlow > g.MaxSlowRate {
		add("candidate_slow_rate_exceeded")
		fail = true
	}
	if cerrSlow-serrSlow > g.MaxSlowRateIncrease {
		add("candidate_slow_rate_increase_exceeded")
		fail = true
	}
	if fail {
		return "FAIL", reasons
	}
	add("within_gate")
	return "PASS", reasons
}
