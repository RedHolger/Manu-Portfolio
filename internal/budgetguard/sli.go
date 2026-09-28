// sli.go — SLI math (§5.3). Eligible population excludes client_error.
// All upstream errors, timeouts, transport errors are eligible BAD requests.
//
// N        = eligible count
// G_avail  = successful eligible count
// G_lat    = successful eligible completed within threshold
// availability = G_avail / N ; latencySLI = G_lat / N
// bad_ratio = 1 - good/N ; burn = bad_ratio / (1-target)
// allowed_bad = N*(1-target) ; remaining = 1 - bad/allowed_bad (may be <0)
package budgetguard

import (
	"math"
)

// Counts is one slot's observed population over a window.
type Counts struct {
	Eligible int64 // N
	Good     int64 // successful eligible (availability good)
	FastGood int64 // successful eligible within threshold (latency good)
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

// Evaluate computes both SLIs. N=0, negative inputs, or nonfinite values
// yield UNKNOWN — never 100% (no-traffic is not perfect availability).
func Evaluate(c Counts, availTarget, latTarget float64) SLIResult {
	if c.Eligible <= 0 {
		return SLIResult{Unknown: true, WhyUnknown: "no eligible traffic in window"}
	}
	if c.Good < 0 || c.FastGood < 0 || c.Good > c.Eligible || c.FastGood > c.Good {
		return SLIResult{Unknown: true, WhyUnknown: "inconsistent counts (good<=eligible, fast<=good required)"}
	}
	r := SLIResult{
		Availability:    float64(c.Good) / float64(c.Eligible),
		Latency:         float64(c.FastGood) / float64(c.Eligible),
		AllowedBadAvail: float64(c.Eligible) * (1 - availTarget),
		AllowedBadLat:   float64(c.Eligible) * (1 - latTarget),
	}
	r.BadRatioAvail = 1 - r.Availability
	r.BadRatioLat = 1 - r.Latency
	r.BurnAvail = r.BadRatioAvail / (1 - availTarget)
	r.BurnLat = r.BadRatioLat / (1 - latTarget)
	badAvail := float64(c.Eligible - c.Good)
	badLat := float64(c.Eligible - c.FastGood)
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
	if stable.Eligible < g.MinRequestsPerSlot || cand.Eligible < g.MinRequestsPerSlot {
		add("insufficient_traffic")
		return "INCONCLUSIVE", reasons
	}
	serr := float64(stable.Eligible-stable.Good) / float64(stable.Eligible)
	serrSlow := float64(stable.Eligible-stable.FastGood) / float64(stable.Eligible)
	if serr > g.MaxErrorRate || serrSlow > g.MaxSlowRate {
		add("baseline_unhealthy")
		return "INCONCLUSIVE", reasons // never blame candidate for shared failure
	}
	cerr := float64(cand.Eligible-cand.Good) / float64(cand.Eligible)
	cerrSlow := float64(cand.Eligible-cand.FastGood) / float64(cand.Eligible)
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
