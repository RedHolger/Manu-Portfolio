// runner.go — F2 experiment runner: phases, abort, cleanup, reconcile.
// Invariants: intent journaled before mutation; one experiment per service
// (journal lock); cleanup on a FRESH bounded context, never the cancelled
// one; TTL expiry is gateway-enforced and independent of this process.
package faultlab

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

	// Ledger enables declared spec.assertions.* checks (F4 oracle). A
	// scenario that declares an assertion without a configured ledger can
	// never reach PASSED — assertions fail closed, never silently skip.
	Ledger Ledger
	// SKU is the inventory the oracle checks (default DefaultSKU).
	SKU string

	// CheckContext verifies the kube context before any mutation.
	// Defaults to a kubectl lookup; tests inject fakes.
	CheckContext func(ctx context.Context) error

	LoadTimeout time.Duration
	CleanupCap  time.Duration

	mu       sync.Mutex
	phases   map[string][]PhaseResult     // runID → persisted phase summaries
	attempts map[string][]loadgen.Attempt // runID+"|"+phase → attempts
}

// DefaultSKU is the workload SKU the load generator offers and the SKU the
// correctness oracle audits.
const DefaultSKU = "demo-item"

// PhaseResult is the persisted outcome of one workload phase. Every phase
// keeps its own summary: an invalid or truncated phase is evidence, and an
// invalid phase can never be part of a PASSED run.
type PhaseResult struct {
	Phase   string          `json:"phase"`
	Summary loadgen.Summary `json:"summary"`
}

func (r *Runner) sku() string {
	if r.SKU == "" {
		return DefaultSKU
	}
	return r.SKU
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
func (r *Runner) Run(ctx context.Context, sc Scenario, runID string) (term string, err error) {
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
	// cleanup path on a FRESH context (cancellation-safe) and REPORTS the
	// state it reached instead of an empty terminal.
	terminal := ""
	defer func() {
		if terminal == "" {
			fresh, cancel := context.WithTimeout(context.WithoutCancel(ctx), r.cleanupCap())
			t, _ := r.toCleaning(fresh, runID, sc, cur, "interrupted")
			cancel()
			terminal = t
		}
		term = terminal
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
			t, _ := r.toCleaning(context.WithoutCancel(ctx), runID, sc, cur, "context: "+err.Error())
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
		t, _ := r.toCleaning(context.WithoutCancel(ctx), runID, sc, cur, "preflight: "+perr.Error())
		terminal = t
		return terminal, fmt.Errorf("preflight: %w", perr)
	}

	if err := step(StBaseline, "baseline-start", "{}"); err != nil {
		return "", err
	}
	if err := r.runPhase(ctx, sc, runID, "baseline", sc.Baseline); err != nil {
		t, _ := r.toCleaning(context.WithoutCancel(ctx), runID, sc, cur, err.Error())
		terminal = t
		return terminal, err
	}

	if err := step(StInjecting, "inject", fmt.Sprintf(`{"kind":%q}`, sc.FaultKind)); err != nil {
		return "", err
	}
	if err := r.applyFault(ctx, j, runID, sc); err != nil {
		t, _ := r.toCleaning(context.WithoutCancel(ctx), runID, sc, cur, "apply: "+err.Error())
		terminal = t
		return terminal, err
	}

	if err := step(StObserving, "observe-start", "{}"); err != nil {
		return "", err
	}
	outcome := r.observe(ctx, sc, runID)
	if outcome != "" {
		t, _ := r.toCleaning(context.WithoutCancel(ctx), runID, sc, cur, outcome)
		terminal = t
		return terminal, fmt.Errorf("observing ended: %s", outcome)
	}
	t, verr := r.verify(context.WithoutCancel(ctx), runID, sc, cur)
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

// phaseSeedOffset keeps per-phase operation/key namespaces disjoint: no
// phase may re-offer another phase's keys (contract §5/§7). Baseline keeps
// the scenario seed for compatibility; later phases offset by a fixed
// stride larger than any bounded run.
func phaseSeedOffset(phase string) int64 {
	switch phase {
	case "fault":
		return 1000000
	case "recovery":
		return 2000000
	}
	return 0
}

// load runs one workload phase. When the scenario declared assertions the
// attempts are also collected for the correctness oracle.
func (r *Runner) load(ctx context.Context, sc Scenario, runID, phase string, secs int64) (loadgen.Summary, error) {
	// Attempts are always recorded: recovery health and the oracle read
	// them, and every phase keeps its own request accounting.
	rn := &loadgen.Runner{BaseURL: r.Gateway, Out: nil}
	rn.OnAttempt = func(a loadgen.Attempt) { r.addAttempt(runID, phase, a) }
	return rn.Run(ctx, loadgen.Config{
		Rate: sc.Rate, Duration: time.Duration(secs) * time.Second,
		Seed: sc.Seed + phaseSeedOffset(phase), Timeout: time.Duration(sc.Timeout) * time.Second,
	})
}

// addAttempt records one attempt for oracle and health analysis.
func (r *Runner) addAttempt(runID, phase string, a loadgen.Attempt) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.attempts == nil {
		r.attempts = map[string][]loadgen.Attempt{}
	}
	r.attempts[runID+"|"+phase] = append(r.attempts[runID+"|"+phase], a)
}

// phaseAttempts returns a copy of one phase's attempts.
func (r *Runner) phaseAttempts(runID, phase string) []loadgen.Attempt {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]loadgen.Attempt(nil), r.attempts[runID+"|"+phase]...)
}

// recordPhase persists one phase summary (finding E). The journal event and
// OutDir/phases.json are evidence: a write failure is an error, never a
// warning that lets the run continue as if the phase had not happened.
func (r *Runner) recordPhase(runID, phase string, sum loadgen.Summary) error {
	pr := PhaseResult{Phase: phase, Summary: sum}
	r.mu.Lock()
	if r.phases == nil {
		r.phases = map[string][]PhaseResult{}
	}
	r.phases[runID] = append(r.phases[runID], pr)
	all := append([]PhaseResult(nil), r.phases[runID]...)
	r.mu.Unlock()
	payload, err := json.Marshal(pr)
	if err != nil {
		return err
	}
	if err := r.Journal.AppendEvent(runID, "phase-load", string(payload)); err != nil {
		return fmt.Errorf("journal phase-load: %w", err)
	}
	if r.OutDir == "" {
		return nil
	}
	raw, err := json.MarshalIndent(map[string]any{"run": runID, "phases": all}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(r.OutDir, "phases.json"), append(raw, '\n'), 0o644); err != nil {
		return fmt.Errorf("write phases.json: %w", err)
	}
	return nil
}

// runPhase executes one workload phase, persists its summary, and enforces
// its delivery validity. An invalid or truncated workload is evidence of a
// failed experiment — it can never contribute to PASSED.
func (r *Runner) runPhase(ctx context.Context, sc Scenario, runID, phase string, secs int64) error {
	sum, err := r.load(ctx, sc, runID, phase, secs)
	if perr := r.recordPhase(runID, phase, sum); perr != nil {
		return perr
	}
	if err != nil {
		return fmt.Errorf("%s load: %w", phase, err)
	}
	if !sum.Valid {
		return fmt.Errorf("invalid workload (%s): %s", phase, sum.InvalidReason)
	}
	return nil
}

// invalidPhases re-checks every persisted phase (belt and braces: each phase
// is enforced when it runs; this stops a PASSED verdict if any phase slipped
// through).
func (r *Runner) invalidPhases(runID string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, p := range r.phases[runID] {
		if !p.Summary.Valid {
			return fmt.Sprintf("invalid workload (%s): %s", p.Phase, p.Summary.InvalidReason)
		}
	}
	return ""
}

// observe runs fault-duration load with 1s abort/telemetry policing.
// Returns "" on clean completion, else the reason for cleanup. Every path
// persists the fault phase summary when one exists (aborted and cancelled
// runs keep their evidence too).
func (r *Runner) observe(ctx context.Context, sc Scenario, runID string) string {
	type res struct {
		sum loadgen.Summary
		err error
	}
	done := make(chan res, 1)
	lctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		sum, err := r.load(lctx, sc, runID, "fault", sc.FaultSecs)
		done <- res{sum, err}
	}()
	// finalize persists the summary and folds load errors or an invalid
	// workload into the returned cleanup reason.
	finalize := func(out res, reason string) string {
		if perr := r.recordPhase(runID, "fault", out.sum); perr != nil {
			if reason == "" {
				reason = "persist fault summary: " + perr.Error()
			} else {
				reason += "; persist fault summary: " + perr.Error()
			}
			return reason
		}
		if reason != "" {
			return reason
		}
		if out.err != nil {
			return "load: " + out.err.Error()
		}
		if !out.sum.Valid {
			return "invalid workload (fault): " + out.sum.InvalidReason
		}
		return ""
	}
	// drain waits (bounded) for the cancelled load so its truncated summary
	// is still recorded as evidence.
	drain := func(reason string) string {
		cancel()
		select {
		case out := <-done:
			return finalize(out, reason)
		case <-time.After(5 * time.Second):
			return reason
		}
	}
	obs := NewObserver(r.Gateway+"/metrics", sc.Slot)
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	strikes := 0
	for {
		select {
		case <-ctx.Done():
			return drain("cancelled")
		case out := <-done:
			return finalize(out, "")
		case <-tick.C:
			if _, err := obs.Scrape(ctx); err != nil {
				strikes++
				if strikes >= 3 {
					return drain("telemetry lost: " + err.Error())
				}
				continue
			}
			strikes = 0
			if obs.AbortTripped(sc.AbortMax, int(sc.AbortN), time.Duration(sc.AbortWin)*time.Second) {
				return drain(fmt.Sprintf("abort: failure ratio above %.0f%%", sc.AbortMax*100))
			}
		}
	}
}

// toCleaning runs the uniform cleanup path on the GIVEN (fresh, bounded)
// context: clear recorded faults, verify zero active, then verify recovery.
func (r *Runner) toCleaning(ctx context.Context, runID string, sc Scenario, cur, reason string) (string, error) {
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
	return r.finishVerify(ctx, runID, sc, reason)
}

// markCleanupFailed records CLEANUP_FAILED (never a success state). Journal
// write failures are reported, not discarded.
func (r *Runner) markCleanupFailed(ctx context.Context, runID, cur, reason string) (string, error) {
	_ = ctx
	j := r.Journal
	var jerr error
	walk := func(from, to, event string) {
		if jerr != nil {
			return
		}
		if err := j.Transition(runID, from, to, event, "{}", ""); err != nil {
			jerr = fmt.Errorf("journal %s->%s: %w", from, to, err)
		}
	}
	from := cur
	if from != StCleaning {
		// Walk to CLEANING first if we are earlier (legal path only).
		for _, s := range []string{StPreflight, StBaseline, StInjecting, StObserving} {
			if from == s {
				walk(from, StCleaning, "cleanup")
				from = StCleaning
				break
			}
		}
	}
	if from == StCreated {
		walk(StCreated, StPreflight, "preflight")
		walk(StPreflight, StCleaning, "cleanup")
		from = StCleaning
	}
	if jerr == nil {
		if err := j.Transition(runID, from, StCleanupF, "cleanup-failed", "{}", reason); err != nil {
			jerr = fmt.Errorf("journal cleanup-failed: %w", err)
		}
	}
	if jerr != nil {
		return StCleanupF, fmt.Errorf("%s; %v", reason, jerr)
	}
	return StCleanupF, fmt.Errorf("%s", reason)
}

// finishVerify lands VERIFYING after a successful cleanup. Cleanup success
// alone is never PASSED (finding D): a non-clean observation reason fails
// immediately; otherwise the configured recovery phase, workload validity,
// and any declared assertions decide the verdict.
func (r *Runner) finishVerify(ctx context.Context, runID string, sc Scenario, reason string) (string, error) {
	j := r.Journal
	cleanupDone := time.Now()
	// Cleanup status first: restoration is a separate fact from whether the
	// service then proved healthy.
	if err := j.AppendEvent(runID, "cleanup-ok", `{"restored":true}`); err != nil {
		return "", fmt.Errorf("journal: %w", err)
	}
	if reason != "" && reason != "observe-complete" {
		payload, _ := json.Marshal(map[string]string{
			"reason": reason, "note": "recovery phase not executed",
		})
		if err := j.AppendEvent(runID, "recovery-skipped", string(payload)); err != nil {
			return "", fmt.Errorf("%s; journal: %v", reason, err)
		}
		if err := j.Transition(runID, StVerifying, StFailed, "resolved", "{}", reason); err != nil {
			return "", fmt.Errorf("%s; journal: %v", reason, err)
		}
		return StFailed, fmt.Errorf("%s", reason)
	}
	return r.verifyRecovery(ctx, runID, sc, cleanupDone)
}

// MinRecoverySamples is the smallest post-cleanup request count that can say
// anything about health: below it a zero failure ratio proves nothing.
const MinRecoverySamples = 3

// MaxRecoveryFailureRatio is the recovery phase's own health policy,
// independent of the experiment abort threshold. Recovery previously reused
// Scenario.AbortMax, so a permissive abort setting (e.g. AbortMax=1.0 for a
// dependency-outage run that must not abort mid-fault) let a 100% failed
// recovery phase pass: no failure ratio ever exceeds 1.0. Recovery must
// prove the restored service serves successfully, so it carries a fixed
// strict budget plus a successful-request floor (2xx responses, not merely
// answered requests: an all-4xx recovery has ratio 0 but proves nothing).
const MaxRecoveryFailureRatio = 0.05

// recoveryHealth summarises post-cleanup requests. A 5xx, a timeout, a
// transport failure, or a request with no response is a failure; 4xx are
// counted only as answered (they prove the service is up, not healthy).
type recoveryHealth struct {
	Attempts int     `json:"attempts"`
	Success  int     `json:"success"`
	Failures int     `json:"failures"`
	Ratio    float64 `json:"failure_ratio"`
}

func healthOf(attempts []loadgen.Attempt) recoveryHealth {
	var h recoveryHealth
	for _, a := range attempts {
		h.Attempts++
		failed := a.Code == 0 || a.Code >= 500 ||
			a.ErrClass == "timeout" || a.ErrClass == "transport"
		switch {
		case failed:
			h.Failures++
		case a.Code >= 200 && a.Code < 300:
			h.Success++
		}
	}
	if h.Attempts > 0 {
		h.Ratio = float64(h.Failures) / float64(h.Attempts)
	}
	return h
}

// verifyRecovery executes the configured recovery phase on the restored
// service, persists its health, then decides PASSED or FAILED. Cleanup
// restoration and post-cleanup health are separate journal events.
func (r *Runner) verifyRecovery(ctx context.Context, runID string, sc Scenario, cleanupDone time.Time) (string, error) {
	j := r.Journal
	fail := func(reason string) (string, error) {
		payload, _ := json.Marshal(map[string]string{"reason": reason})
		if err := j.AppendEvent(runID, "recovery-failed", string(payload)); err != nil {
			return "", fmt.Errorf("%s; journal: %v", reason, err)
		}
		if err := j.Transition(runID, StVerifying, StFailed, "resolved", "{}", reason); err != nil {
			return "", fmt.Errorf("%s; journal: %v", reason, err)
		}
		return StFailed, fmt.Errorf("%s", reason)
	}

	rctx, cancel := context.WithTimeout(ctx, time.Duration(sc.Recover+60)*time.Second)
	defer cancel()
	loadErr := r.runPhase(rctx, sc, runID, "recovery", sc.Recover)
	attempts := r.phaseAttempts(runID, "recovery")
	h := healthOf(attempts)
	healthPayload, err := json.Marshal(struct {
		Phase  string         `json:"phase"`
		Health recoveryHealth `json:"health"`
	}{Phase: "recovery", Health: h})
	if err != nil {
		return "", fmt.Errorf("marshal recovery health: %w", err)
	}
	if err := j.AppendEvent(runID, "recovery-health", string(healthPayload)); err != nil {
		return "", fmt.Errorf("journal: %w", err)
	}
	if loadErr != nil {
		return fail(loadErr.Error())
	}
	if reason := r.invalidPhases(runID); reason != "" {
		return fail(reason)
	}
	if h.Attempts < MinRecoverySamples {
		return fail(fmt.Sprintf("recovery not measurable: %d post-cleanup requests, need >= %d",
			h.Attempts, MinRecoverySamples))
	}
	if h.Ratio > MaxRecoveryFailureRatio {
		return fail(fmt.Sprintf("service not healthy after cleanup: %.0f%% of %d post-cleanup requests failed (recovery budget %.0f%%)",
			h.Ratio*100, h.Attempts, MaxRecoveryFailureRatio*100))
	}
	if h.Success < MinRecoverySamples {
		return fail(fmt.Sprintf("recovery not proven: %d successful post-cleanup requests, need >= %d",
			h.Success, MinRecoverySamples))
	}
	if sc.RecoveryDeadline > 0 {
		elapsed := time.Since(cleanupDone)
		if elapsed > time.Duration(sc.RecoveryDeadline)*time.Second {
			return fail(fmt.Sprintf("recovery exceeded deadline: %s > %ds",
				elapsed.Round(time.Millisecond), sc.RecoveryDeadline))
		}
	}
	if sc.AssertsDeclared {
		if reason, oerr := r.runOracle(ctx, runID, sc); oerr != nil {
			return "", oerr // journal/persistence failure: state unknown
		} else if reason != "" {
			return fail(reason)
		}
	}
	if err := j.AppendEvent(runID, "recovery-ok",
		fmt.Sprintf(`{"attempts":%d,"failure_ratio":%.6f}`, h.Attempts, h.Ratio)); err != nil {
		return "", fmt.Errorf("journal: %w", err)
	}
	if err := j.Transition(runID, StVerifying, StPassed, "resolved", "{}", ""); err != nil {
		return "", fmt.Errorf("journal: %w", err)
	}
	return StPassed, nil
}

// runOracle evaluates the declared assertions against the fault lab's
// inventory ledger using this run's attempts. It returns a failure reason
// (empty when clean) or an error for persistence problems. Missing ledger
// configuration with assertions declared fails closed.
func (r *Runner) runOracle(ctx context.Context, runID string, sc Scenario) (string, error) {
	j := r.Journal
	if r.Ledger == nil {
		return "assertions declared but no ledger configured (--pg-dsn): oracle cannot run", nil
	}
	findings, oerr := Check(ctx, r.Ledger, r.sku(), r.oracleOps(runID))
	payload, err := json.Marshal(map[string]any{
		"clean":      oerr == nil && findings.Clean(),
		"violations": findings.Violations,
		"info":       findings.Info,
		"sku":        r.sku(),
		"scenario":   sc.Name,
	})
	if err != nil {
		return "", fmt.Errorf("marshal oracle findings: %w", err)
	}
	if err := j.AppendEvent(runID, "oracle", string(payload)); err != nil {
		return "", fmt.Errorf("journal oracle: %w", err)
	}
	if r.OutDir != "" {
		if err := os.WriteFile(filepath.Join(r.OutDir, "oracle.json"), append(payload, '\n'), 0o644); err != nil {
			return "", fmt.Errorf("write oracle.json: %w", err)
		}
	}
	if oerr != nil {
		return "oracle: " + oerr.Error(), nil
	}
	if !findings.Clean() {
		return "oracle violations: " + strings.Join(findings.Violations, "; "), nil
	}
	return "", nil
}

// oracleOps converts this run's recorded attempts (in phase order) to
// ledger operations for the correctness check.
func (r *Runner) oracleOps(runID string) []OpRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	var ops []OpRecord
	for _, phase := range []string{"baseline", "fault", "recovery"} {
		for _, a := range r.attempts[runID+"|"+phase] {
			ops = append(ops, OpRecord{
				OpID: a.OpID, Key: a.Key, SKU: a.SKU, Qty: a.Qty,
				Acked: a.Code >= 200 && a.Code < 300, ResvID: a.ResvID,
			})
		}
	}
	return ops
}

// verify is the clean-path VERIFYING step after full observation.
func (r *Runner) verify(ctx context.Context, runID string, sc Scenario, cur string) (string, error) {
	return r.toCleaning(ctx, runID, sc, cur, "observe-complete")
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
