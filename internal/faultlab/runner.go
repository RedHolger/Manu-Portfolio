// runner.go — F2 experiment runner: phases, abort, cleanup, reconcile.
// Invariants: intent journaled before mutation; one experiment per service
// (journal lock); cleanup on a FRESH bounded context, never the cancelled
// one; TTL expiry is gateway-enforced and independent of this process.
package faultlab

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"sre-portfolio/internal/clock"
	"sre-portfolio/internal/loadgen"
)

// Runner executes one experiment end to end.
type Runner struct {
	Journal  *Journal
	Injector Injector
	Clock    clock.Clock
	Gateway  string // public base URL for load + metrics
	OutDir   string

	// Pods enables pod_delete faults; Dep enables dependency_failure faults.
	// Nil backends reject their kinds BEFORE any mutation (no journal row).
	Pods *PodDeleter
	Dep  DepFaultCtl

	// CheckContext verifies the kube context before any mutation.
	// Defaults to a kubectl lookup; tests inject fakes.
	CheckContext func(ctx context.Context) error

	LoadTimeout time.Duration
	CleanupCap  time.Duration
}

// slotDeployment maps experiment slots to lab Deployments.
var slotDeployment = map[string]string{"stable": "api-stable", "candidate": "api-candidate"}

// backendFor validates the required backend exists for the fault kind,
// before any journal row or lock is created.
func (r *Runner) backendFor(kind string) error {
	switch kind {
	case FaultDelay, FaultConnFail:
		return nil // gateway Injector always configured
	case FaultPodDelete:
		if r.Pods == nil {
			return fmt.Errorf("pod faults need Pods configured (--kubeconfig)")
		}
		return nil
	case FaultDepOutage:
		if r.Dep == nil {
			return fmt.Errorf("dependency faults need Dep configured (--api-admin)")
		}
		return nil
	default:
		return fmt.Errorf("unsupported fault kind %q", kind)
	}
}

// faultIDFor derives the single fault ID for a run.
func faultIDFor(runID string) string { return runID + "-f1" }

// Run executes the full lifecycle, returning the terminal state.
func (r *Runner) Run(ctx context.Context, sc Scenario, runID string) (string, error) {
	j := r.Journal
	if err := r.backendFor(sc.FaultKind); err != nil {
		return "", err // unsupported kind: no journal row, no lock, no mutation
	}
	deadline := time.Now().Add(time.Duration(sc.Baseline+sc.FaultSecs+sc.Recover+120) * time.Second)
	if err := j.CreateRun(runID, sc.Name, sc.Service, sc.Seed, deadline); err != nil {
		return "", err // locked or duplicate: no mutation attempted
	}
	cur := StCreated
	// Safety net: any early return without a terminal state forces the
	// cleanup path on a FRESH context (cancellation-safe).
	terminal := ""
	defer func() {
		if terminal != "" {
			return
		}
		fresh, cancel := context.WithTimeout(context.WithoutCancel(ctx), r.cleanupCap())
		defer cancel()
		t, _ := r.toCleaning(fresh, runID, cur, "interrupted")
		terminal = t
	}()
	step := func(to, event, payload string) error {
		if err := j.Transition(runID, cur, to, event, payload, ""); err != nil {
			return err
		}
		cur = to
		return nil
	}

	if r.CheckContext != nil {
		if err := r.CheckContext(ctx); err != nil {
			t, _ := r.toCleaning(context.WithoutCancel(ctx), runID, cur, "context: "+err.Error())
			terminal = t
			return terminal, fmt.Errorf("context refused: %w", err)
		}
	}
	if err := step(StPreflight, "preflight", "{}"); err != nil {
		return "", err
	}
	// Preflight probe: injector reachable before any load runs.
	pctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	_, perr := r.Injector.Active(pctx)
	cancel()
	if perr != nil {
		t, _ := r.toCleaning(context.WithoutCancel(ctx), runID, cur, "preflight: "+perr.Error())
		terminal = t
		return terminal, fmt.Errorf("preflight: %w", perr)
	}

	if err := step(StBaseline, "baseline-start", "{}"); err != nil {
		return "", err
	}
	if _, err := r.load(ctx, sc, "baseline", sc.Baseline); err != nil {
		t, _ := r.toCleaning(context.WithoutCancel(ctx), runID, cur, "baseline load: "+err.Error())
		terminal = t
		return terminal, err
	}

	if err := step(StInjecting, "inject", fmt.Sprintf(`{"kind":%q}`, sc.FaultKind)); err != nil {
		return "", err
	}
	if err := r.applyFault(ctx, j, runID, sc); err != nil {
		t, _ := r.toCleaning(context.WithoutCancel(ctx), runID, cur, "apply: "+err.Error())
		terminal = t
		return terminal, err
	}

	if err := step(StObserving, "observe-start", "{}"); err != nil {
		return "", err
	}
	outcome := r.observe(ctx, sc)
	if outcome != "" {
		t, _ := r.toCleaning(context.WithoutCancel(ctx), runID, cur, outcome)
		terminal = t
		return terminal, fmt.Errorf("observing ended: %s", outcome)
	}
	t, verr := r.verify(context.WithoutCancel(ctx), runID, cur)
	terminal = t
	return terminal, verr
}

// applyFault records intent, mutates, and records application — per kind.
func (r *Runner) applyFault(ctx context.Context, j *Journal, runID string, sc Scenario) error {
	fid := faultIDFor(runID)
	expires := time.Now().Add(time.Duration(sc.TTL) * time.Second).UTC().Format(time.RFC3339Nano)
	switch sc.FaultKind {
	case FaultDelay, FaultConnFail:
		in := Injection{ID: fid, Kind: sc.FaultKind, Slot: sc.Slot,
			DelayMs: sc.DelayMs, Fraction: sc.Fraction, TTL: sc.TTL}
		if err := j.RecordFault(runID, in.ID, in.Kind,
			fmt.Sprintf(`{"slot":%q,"fraction":%v}`, in.Slot, in.Fraction), expires); err != nil {
			return err
		}
		if err := r.Injector.Apply(ctx, in); err != nil {
			return err
		}
		return j.MarkApplied(runID, in.ID)
	case FaultPodDelete:
		dep, ok := slotDeployment[sc.Slot]
		if !ok {
			return fmt.Errorf("no deployment for slot %q", sc.Slot)
		}
		tgt, err := r.Pods.PickTarget(ctx, dep)
		if err != nil {
			return err
		}
		targetJSON := fmt.Sprintf(`{"slot":%q,"deployment":%q,"pod":%q,"uid":%q}`,
			sc.Slot, tgt.Deployment, tgt.PodName, tgt.UID)
		if err := j.RecordFault(runID, fid, sc.FaultKind, targetJSON, expires); err != nil {
			return err
		}
		if err := r.Pods.Delete(ctx, tgt); err != nil {
			return err
		}
		return j.MarkApplied(runID, fid)
	case FaultDepOutage:
		if err := j.RecordFault(runID, fid, sc.FaultKind,
			fmt.Sprintf(`{"slot":%q,"ttl":%d}`, sc.Slot, sc.TTL), expires); err != nil {
			return err
		}
		if err := r.Dep.SetFail(ctx, sc.TTL); err != nil {
			return err
		}
		return j.MarkApplied(runID, fid)
	default:
		return fmt.Errorf("unsupported fault kind %q", sc.FaultKind)
	}
}

// podTargetOf decodes a journaled pod target.
func podTargetOf(f FaultRow) (PodTarget, error) {
	var t struct {
		Deployment string `json:"deployment"`
		Pod        string `json:"pod"`
		UID        string `json:"uid"`
	}
	if err := json.Unmarshal([]byte(f.TargetJSON), &t); err != nil {
		return PodTarget{}, err
	}
	if t.Deployment == "" || t.Pod == "" || t.UID == "" {
		return PodTarget{}, fmt.Errorf("fault %s missing pod target identity", f.ID)
	}
	return PodTarget{Deployment: t.Deployment, PodName: t.Pod, UID: t.UID}, nil
}

// clearFault removes one recorded fault and verifies its kind-specific
// restoration. Duplicate calls are harmless (idempotent primitives).
func (r *Runner) clearFault(ctx context.Context, runID string, f FaultRow) error {
	switch f.Kind {
	case FaultDelay, FaultConnFail:
		return r.Injector.Clear(ctx, f.ID)
	case FaultPodDelete:
		tgt, err := podTargetOf(f)
		if err != nil {
			return err
		}
		// The pod should already be gone; if the same UID persists, one
		// bounded re-delete with the RECORDED identity (never a fresh
		// resolve, which could target a different pod).
		if verr := r.Pods.VerifyGone(ctx, tgt); verr != nil {
			if derr := r.Pods.Delete(ctx, tgt); derr != nil {
				return derr
			}
			return r.Pods.VerifyGone(ctx, tgt)
		}
		return nil
	case FaultDepOutage:
		if err := r.Dep.Clear(ctx); err != nil {
			return err
		}
		bad, err := r.Dep.IsFailing(ctx)
		if err != nil {
			return err
		}
		if bad {
			return fmt.Errorf("dependency fault still active after clear")
		}
		return nil
	default:
		return fmt.Errorf("unsupported fault kind %q", f.Kind)
	}
}

// phaseSeedOffset keeps per-phase operation/key namespaces disjoint: the
// fault phase must not re-offer baseline keys as accidental replays
// (contract §5/§7). Baseline keeps the scenario seed for compatibility;
// later phases offset by a fixed stride larger than any bounded run.
func phaseSeedOffset(phase string) int64 {
	if phase == "fault" {
		return 1000000
	}
	return 0
}

// load runs one workload phase.
func (r *Runner) load(ctx context.Context, sc Scenario, phase string, secs int64) (loadgen.Summary, error) {
	rn := &loadgen.Runner{BaseURL: r.Gateway, Out: nil}
	return rn.Run(ctx, loadgen.Config{
		Rate: sc.Rate, Duration: time.Duration(secs) * time.Second,
		Seed: sc.Seed + phaseSeedOffset(phase), Timeout: time.Duration(sc.Timeout) * time.Second,
	})
}

// observe runs fault-duration load with 1s abort/telemetry polling.
// Returns "" on clean completion, else the reason for cleanup.
func (r *Runner) observe(ctx context.Context, sc Scenario) string {
	type res struct {
		sum loadgen.Summary
		err error
	}
	done := make(chan res, 1)
	lctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		sum, err := r.load(lctx, sc, "fault", sc.FaultSecs)
		done <- res{sum, err}
	}()
	obs := NewObserver(r.Gateway+"/metrics", sc.Slot)
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	strikes := 0
	for {
		select {
		case <-ctx.Done():
			cancel()
			return "cancelled"
		case out := <-done:
			if out.err != nil {
				return "load: " + out.err.Error()
			}
			return "" // full fault window observed without abort
		case <-tick.C:
			if _, err := obs.Scrape(ctx); err != nil {
				strikes++
				if strikes >= 3 {
					cancel()
					return "telemetry lost: " + err.Error()
				}
				continue
			}
			strikes = 0
			if obs.AbortTripped(sc.AbortMax, int(sc.AbortN), time.Duration(sc.AbortWin)*time.Second) {
				cancel()
				return fmt.Sprintf("abort: failure ratio above %.0f%%", sc.AbortMax*100)
			}
		}
	}
}

// toCleaning runs the uniform cleanup path on the GIVEN (fresh, bounded)
// context: clear recorded faults, verify zero active, then verify recovery.
func (r *Runner) toCleaning(ctx context.Context, runID, cur, reason string) (string, error) {
	j := r.Journal
	if cur == StCreated || cur == StPreflight || cur == StBaseline ||
		cur == StInjecting || cur == StObserving {
		if err := j.Transition(runID, cur, StCleaning, "cleanup", fmt.Sprintf(`{"reason":%q}`, reason), ""); err != nil {
			return r.currentState(runID)
		}
		cur = StCleaning
	}
	faults, err := j.Faults(runID)
	if err != nil {
		return r.markCleanupFailed(ctx, runID, cur, "journal: "+err.Error())
	}
	for _, f := range faults {
		if f.ClearedAt != "" {
			continue
		}
		// Kind-dispatched, idempotent cleanup (duplicate calls harmless).
		if cerr := r.clearFault(ctx, runID, f); cerr != nil {
			return r.markCleanupFailed(ctx, runID, cur, "clear: "+cerr.Error())
		}
		if merr := j.MarkCleared(runID, f.ID); merr != nil {
			return r.markCleanupFailed(ctx, runID, cur, "journal: "+merr.Error())
		}
	}
	live := []string(nil)
	var liveErr error
	for _, f := range faults {
		if f.Kind == FaultDelay || f.Kind == FaultConnFail {
			live, liveErr = r.Injector.Active(ctx)
			break
		}
	}
	if liveErr != nil || len(live) != 0 {
		msg := fmt.Sprintf("unverified restoration (live=%v err=%v)", live, liveErr)
		return r.markCleanupFailed(ctx, runID, cur, msg)
	}
	if err := j.Transition(runID, StCleaning, StVerifying, "verify", "{}", ""); err != nil {
		return r.currentState(runID)
	}
	return r.finishVerify(ctx, runID, reason)
}

// markCleanupFailed records CLEANUP_FAILED (never a success state).
func (r *Runner) markCleanupFailed(ctx context.Context, runID, cur, reason string) (string, error) {
	_ = ctx
	from := cur
	if from != StCleaning {
		// Walk to CLEANING first if we are earlier (legal path only).
		for _, s := range []string{StPreflight, StBaseline, StInjecting, StObserving} {
			if from == s {
				_ = r.Journal.Transition(runID, from, StCleaning, "cleanup", "{}", "")
				from = StCleaning
				break
			}
		}
	}
	if from == StCreated {
		_ = r.Journal.Transition(runID, StCreated, StPreflight, "preflight", "{}", "")
		_ = r.Journal.Transition(runID, StPreflight, StCleaning, "cleanup", "{}", "")
		from = StCleaning
	}
	_ = r.Journal.Transition(runID, from, StCleanupF, "cleanup-failed", "{}", reason)
	return StCleanupF, fmt.Errorf("%s", reason)
}

// finishVerify checks post-cleanup health, then PASSED or FAILED.
func (r *Runner) finishVerify(ctx context.Context, runID, reason string) (string, error) {
	if reason != "" && reason != "observe-complete" {
		_ = r.Journal.Transition(runID, StVerifying, StFailed, "resolved", "{}", reason)
		return StFailed, fmt.Errorf("%s", reason)
	}
	_ = r.Journal.Transition(runID, StVerifying, StPassed, "resolved", "{}", "")
	return StPassed, nil
}

// verify is the clean-path VERIFYING step after full observation.
func (r *Runner) verify(ctx context.Context, runID, cur string) (string, error) {
	return r.toCleaning(ctx, runID, cur, "observe-complete")
}

// Reconcile recovers unfinished runs (F2): clear recorded faults, verify
// restoration, and land each run in FAILED (interrupted) or CLEANUP_FAILED.
// Uses the given context directly — callers pass a fresh bounded one.
func (r *Runner) Reconcile(ctx context.Context) ([]string, error) {
	un, err := r.Journal.Unfinished()
	if err != nil {
		return nil, err
	}
	var done []string
	for _, run := range un {
		if err := r.reconcileOne(ctx, run); err != nil {
			return done, fmt.Errorf("run %s: %w", run.ID, err)
		}
		done = append(done, run.ID)
	}
	return done, nil
}

func (r *Runner) reconcileOne(ctx context.Context, run Run) error {
	j := r.Journal
	faults, err := j.Faults(run.ID)
	if err != nil {
		return err
	}
	for _, f := range faults {
		if f.ClearedAt != "" {
			continue
		}
		if cerr := r.clearFault(ctx, run.ID, f); cerr != nil {
			_, merr := r.forceFailed(run.ID, "reconcile clear: "+cerr.Error())
			if merr != nil {
				return merr
			}
			return nil
		}
		if merr := j.MarkCleared(run.ID, f.ID); merr != nil {
			return merr
		}
	}
	// Gateway liveness is only meaningful when gateway faults were used;
	// pod/dep runs must not fail reconcile on an unrelated gateway outage.
	hasGateway := false
	for _, f := range faults {
		if f.Kind == FaultDelay || f.Kind == FaultConnFail {
			hasGateway = true
			break
		}
	}
	if hasGateway {
		live, err := r.Injector.Active(ctx)
		if err != nil || len(live) != 0 {
			_, merr := r.forceFailed(run.ID,
				fmt.Sprintf("reconcile unverified (live=%v err=%v)", live, err))
			if merr != nil {
				return merr
			}
			return nil
		}
	}
	_, merr := r.forceFailed(run.ID, "interrupted (reconciled)")
	return merr
}

// forceFailed walks the legal path to a terminal FAILED/CLEANUP_FAILED and
// reports the LANDED state. Mechanical failures (journal errors) return err;
// landing in FAILED is a successful reconcile (nil error) — callers must
// not treat the terminal reason as a mechanical failure, or one
// interrupted run would abort reconciliation of the rest.
func (r *Runner) forceFailed(runID, reason string) (string, error) {
	j := r.Journal
	cur, err := j.GetRun(runID)
	if err != nil {
		return "", err
	}
	state := cur.State
	step := func(to string) error {
		if state == to {
			return nil
		}
		if err := j.Transition(runID, state, to, "reconcile", "{}", ""); err != nil {
			return err
		}
		state = to
		return nil
	}
	clean := reason == "interrupted (reconciled)"
	if state == StCreated {
		if err := step(StPreflight); err != nil {
			return "", err
		}
	}
	if state == StPreflight || state == StBaseline || state == StInjecting || state == StObserving {
		if err := step(StCleaning); err != nil {
			return "", err
		}
	}
	if state == StCleaning {
		if !clean {
			if err := j.Transition(runID, StCleaning, StCleanupF, "reconcile", "{}", reason); err != nil {
				return "", err
			}
			return StCleanupF, nil
		}
		if err := step(StVerifying); err != nil {
			return "", err
		}
	}
	if state == StVerifying {
		if err := j.Transition(runID, StVerifying, StFailed, "reconcile", "{}", reason); err != nil {
			return "", err
		}
		return StFailed, nil
	}
	if Terminal(state) {
		return state, nil
	}
	return "", fmt.Errorf("cannot reconcile from %s", state)
}

// currentState reports the journaled state (race fallback: another actor
// moved the run; report truth instead of forcing).
func (r *Runner) currentState(runID string) (string, error) {
	cur, err := r.Journal.GetRun(runID)
	if err != nil {
		return "", err
	}
	if Terminal(cur.State) && cur.Error != "" {
		return cur.State, fmt.Errorf("%s", cur.Error)
	}
	return cur.State, fmt.Errorf("concurrent transition observed at %s", cur.State)
}

func (r *Runner) cleanupCap() time.Duration {
	if r.CleanupCap > 0 {
		return r.CleanupCap
	}
	return 60 * time.Second
}
