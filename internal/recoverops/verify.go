// verify.go — R4 recovery verification (offline logic, live PENDING).
// Lab recovery criteria (§8 R4): desired template present, observed
// generation advanced, ready/available replicas, plus three nonoverlapping
// 10s traffic windows each with >=100 eligible, >=99% success and >=99%
// joint fast success under 300ms. Bounded to 180s; missing telemetry or
// timeout escalates WITHOUT another action. Pure function for tests; the
// live adapter supplies snapshots and windows from k8s + Prometheus.
package recoverops

import "fmt"

// VerifyWindow is one 10s traffic sample.
type VerifyWindow struct {
	Eligible  int64   `json:"eligible"`
	Success   int64   `json:"success"`
	FastOK    int64   `json:"fast_ok"`
	SuccessRt float64 `json:"success_rate"`
}

// VerifyInput is the observed post-patch state.
type VerifyInput struct {
	DesiredPresent   bool           `json:"desired_present"`
	GenerationHit    bool           `json:"generation_hit"`
	ReadyReplicas    int64          `json:"ready_replicas"`
	WantReplicas     int64          `json:"want_replicas"`
	Windows          []VerifyWindow `json:"windows"`
	TelemetryMissing bool           `json:"telemetry_missing"`
	TimedOut         bool           `json:"timed_out"`
}

// VerifyOutcome is pass/fail with a machine-readable reason.
type VerifyOutcome struct {
	Recovered bool   `json:"recovered"`
	Reason    string `json:"reason"`
}

// VerifyRecovery enforces R4 without touching the cluster.
func VerifyRecovery(in VerifyInput) VerifyOutcome {
	if in.TelemetryMissing {
		return VerifyOutcome{Reason: "telemetry missing → escalate, no further action"}
	}
	if in.TimedOut {
		return VerifyOutcome{Reason: "verification deadline (180s) exceeded → escalate, no further action"}
	}
	if !in.DesiredPresent {
		return VerifyOutcome{Reason: "desired template not present"}
	}
	if !in.GenerationHit {
		return VerifyOutcome{Reason: "observed generation not advanced"}
	}
	if in.WantReplicas < 1 || in.ReadyReplicas < in.WantReplicas {
		return VerifyOutcome{Reason: fmt.Sprintf("replicas not ready (%d/%d)", in.ReadyReplicas, in.WantReplicas)}
	}
	if len(in.Windows) != 3 {
		return VerifyOutcome{Reason: fmt.Sprintf("need exactly 3 nonoverlapping 10s windows, got %d", len(in.Windows))}
	}
	for i, w := range in.Windows {
		if w.Eligible < 100 {
			return VerifyOutcome{Reason: fmt.Sprintf("window %d: only %d eligible, need >=100", i, w.Eligible)}
		}
		if float64(w.Success)/float64(w.Eligible) < 0.99 {
			return VerifyOutcome{Reason: fmt.Sprintf("window %d: success %.3f < 0.99", i, float64(w.Success)/float64(w.Eligible))}
		}
		if float64(w.FastOK)/float64(w.Eligible) < 0.99 {
			return VerifyOutcome{Reason: fmt.Sprintf("window %d: joint fast %.3f < 0.99", i, float64(w.FastOK)/float64(w.Eligible))}
		}
	}
	return VerifyOutcome{Recovered: true, Reason: "recovered: 3/3 windows ≥100 eligible, ≥99% success, ≥99% fast<300ms"}
}
