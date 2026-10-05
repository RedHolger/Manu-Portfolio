// runner_test.go — F2 lifecycle with fakes (no cluster).
package faultlab

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"sre-portfolio/internal/clock"
	"sre-portfolio/internal/loadgen"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// labDouble serves reservations (201) and exposition counters. failEveryNth
// request returns 500 (0 = all healthy).
type labDouble struct {
	srv       *httptest.Server
	total     atomic.Int64
	failed    atomic.Int64
	failEvery int
	slot      string
	down      atomic.Bool
	// hang blocks every reservation request until the client gives up:
	// used to starve a workload phase of completions.
	hang atomic.Bool
	// breakPrefix 500s any reservation whose Idempotency-Key starts with it,
	// which lets a test break exactly one phase (phase key namespaces are
	// disjoint: recovery uses load-<seed+2000000>-N).
	breakPrefix atomic.Pointer[string]
	// breakStatus is served to breakPrefix requests (default 500); a test
	// can set 400 to prove answered-but-never-successful recovery fails.
	breakStatus atomic.Int64
}

func newLabDouble(t *testing.T, slot string, failEvery int) *labDouble {
	t.Helper()
	d := &labDouble{slot: slot, failEvery: failEvery}
	d.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if d.down.Load() {
			hj, ok := w.(http.Hijacker)
			if !ok {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			conn, _, _ := hj.Hijack()
			_ = conn.Close() // transport error, not HTTP status
			return
		}
		if r.URL.Path == "/metrics" {
			total := d.total.Load()
			failed := d.failed.Load()
			fmt.Fprintf(w, `lab_requests_total{service="reservations",slot=%q,route="/v1/reservations",result="success"} %d
lab_requests_total{service="reservations",slot=%q,route="/v1/reservations",result="server_error"} %d
`, d.slot, total-failed, d.slot, failed)
			return
		}
		if p := d.breakPrefix.Load(); p != nil &&
			strings.HasPrefix(r.Header.Get("Idempotency-Key"), *p) {
			d.total.Add(1)
			d.failed.Add(1)
			code := int(d.breakStatus.Load())
			if code == 0 {
				code = http.StatusInternalServerError
			}
			w.WriteHeader(code)
			_, _ = w.Write([]byte(`{"error":"still broken"}`))
			return
		}
		if d.hang.Load() {
			<-r.Context().Done()
			return
		}
		n := d.total.Add(1)
		if failEvery > 0 && int(n)%failEvery == 0 {
			d.failed.Add(1)
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"x"}`))
			return
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"x"}`))
	}))
	t.Cleanup(d.srv.Close)
	return d
}

func testScenario() Scenario {
	return Scenario{
		Name: "t", Context: WantContext, Namespace: WantNamespace,
		Service: "reservations", Slot: "stable",
		Rate: 20, Seed: 1, Timeout: 2,
		Baseline: 1, FaultSecs: 2, Recover: 1,
		FaultKind: FaultDelay, DelayMs: 100, Fraction: 0.5, TTL: 30,
		AbortMax: 0.5, AbortN: 2, AbortWin: 1,
	}
}

func testRunner(t *testing.T, gw string) (*Runner, *Journal, *FakeInjector) {
	t.Helper()
	j, err := Open(filepath.Join(t.TempDir(), "j.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = j.Close() })
	f := NewFakeInjector()
	r := &Runner{
		Journal: j, Injector: f, Clock: clock.RealClock{},
		Gateway: gw, OutDir: t.TempDir(),
		CheckContext: func(ctx context.Context) error { return nil },
		LoadTimeout:  5 * time.Second, CleanupCap: 10 * time.Second,
	}
	// Silence unused loadgen import if runner stops using it directly.
	_ = loadgen.Config{}
	return r, j, f
}

func TestRunnerCleanPass(t *testing.T) {
	d := newLabDouble(t, "stable", 0)
	r, j, f := testRunner(t, d.srv.URL)
	sc := testScenario()
	term, err := r.Run(context.Background(), sc, "run-1")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if term != StPassed {
		t.Fatalf("terminal=%s, want PASSED", term)
	}
	row, _ := j.GetRun("run-1")
	if row.State != StPassed {
		t.Fatalf("journal=%s", row.State)
	}
	if live, _ := f.Active(context.Background()); len(live) != 0 {
		t.Fatalf("faults live after pass: %v", live)
	}
	if n, _ := j.EventCount("run-1"); n < 7 {
		t.Fatalf("events=%d, want full phase trail", n)
	}
	// Lock released: a second run proceeds.
	if _, err := r.Run(context.Background(), sc, "run-2"); err != nil {
		t.Fatalf("second run blocked: %v", err)
	}
}

func TestRunnerAbortOnFailure(t *testing.T) {
	d := newLabDouble(t, "stable", 1) // every request fails
	r, _, f := testRunner(t, d.srv.URL)
	sc := testScenario()
	sc.FaultSecs = 5 // abort needs ~3 one-second ticks to see two windows
	term, err := r.Run(context.Background(), sc, "run-ab")
	if err == nil || !strings.Contains(err.Error(), "abort") {
		t.Fatalf("term=%s err=%v, want abort", term, err)
	}
	if term != StFailed {
		t.Fatalf("terminal=%s, want FAILED", term)
	}
	if live, _ := f.Active(context.Background()); len(live) != 0 {
		t.Fatalf("fault not cleaned after abort: %v", live)
	}
}

func TestRunnerCancelCleansUp(t *testing.T) {
	d := newLabDouble(t, "stable", 0)
	r, j, f := testRunner(t, d.srv.URL)
	sc := testScenario()
	sc.Baseline, sc.FaultSecs = 1, 30
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan string, 1)
	go func() {
		term, _ := r.Run(ctx, sc, "run-cancel")
		done <- term
	}()
	time.Sleep(1500 * time.Millisecond)
	cancel()
	select {
	case term := <-done:
		if term != StFailed && term != StCleanupF {
			t.Fatalf("terminal=%s after cancel", term)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("run did not terminate after cancel")
	}
	row, _ := j.GetRun("run-cancel")
	if !Terminal(row.State) {
		t.Fatalf("non-terminal after cancel: %s", row.State)
	}
	if live, _ := f.Active(context.Background()); len(live) != 0 {
		t.Fatalf("faults live after cancel: %v", live)
	}
}

func TestRunnerLockBlocksConcurrent(t *testing.T) {
	d := newLabDouble(t, "stable", 0)
	r, j, f := testRunner(t, d.srv.URL)
	sc := testScenario()
	if err := j.CreateRun("other", "h", "reservations", 1, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Run(context.Background(), sc, "run-locked"); err == nil {
		t.Fatal("expected lock error")
	}
	if len(f.Applied) != 0 {
		t.Fatalf("mutation attempted despite lock: %+v", f.Applied)
	}
}

func TestReconcileUnfinished(t *testing.T) {
	d := newLabDouble(t, "stable", 0)
	r, j, f := testRunner(t, d.srv.URL)
	_ = d
	if err := j.CreateRun("r-old", "h", "reservations", 1, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	// Simulate a crash mid-INJECTING with a recorded-but-dangling fault.
	if err := j.Transition("r-old", StCreated, StPreflight, "e", "{}", ""); err != nil {
		t.Fatal(err)
	}
	if err := j.Transition("r-old", StPreflight, StInjecting, "e", "{}", ""); err == nil {
		t.Fatal("PREFLIGHT->INJECTING must be illegal (must pass BASELINE)")
	}
	if err := j.Transition("r-old", StPreflight, StBaseline, "e", "{}", ""); err != nil {
		t.Fatal(err)
	}
	if err := j.Transition("r-old", StBaseline, StInjecting, "e", "{}", ""); err != nil {
		t.Fatal(err)
	}
	if err := j.RecordFault("r-old", "r-old-f1", FaultDelay, `{"slot":"stable"}`, "2030-01-01T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if err := f.Apply(context.Background(), Injection{ID: "r-old-f1", Kind: FaultDelay}); err != nil {
		t.Fatal(err)
	}
	done, err := r.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(done) != 1 || done[0] != "r-old" {
		t.Fatalf("reconciled=%v", done)
	}
	row, _ := j.GetRun("r-old")
	if row.State != StFailed {
		t.Fatalf("state=%s, want FAILED", row.State)
	}
	if !strings.Contains(row.Error, "interrupted") {
		t.Fatalf("error=%q, want interrupted note", row.Error)
	}
	if live, _ := f.Active(context.Background()); len(live) != 0 {
		t.Fatalf("dangling fault after reconcile: %v", live)
	}
}

func TestRunnerResultJSON(t *testing.T) {
	// Report shape sanity: journaled evidence serializes.
	d := newLabDouble(t, "stable", 0)
	r, j, _ := testRunner(t, d.srv.URL)
	term, err := r.Run(context.Background(), testScenario(), "run-json")
	if err != nil || term != StPassed {
		t.Fatalf("term=%s err=%v", term, err)
	}
	row, _ := j.GetRun("run-json")
	raw, err := json.Marshal(map[string]any{
		"id": row.ID, "state": row.State, "seed": row.Seed,
	})
	if err != nil || len(raw) == 0 {
		t.Fatal("report marshal failed")
	}
}

// Runner pod-fault integration (fake cluster): pick by ownership, delete by
// UID, verify gone, full lifecycle to PASSED.
func TestRunnerPodFaultPass(t *testing.T) {
	d := newLabDouble(t, "stable", 0)
	r, j, _ := testRunner(t, d.srv.URL)
	cs := fake.NewSimpleClientset(labDeployment("x"), labReplicaSet(),
		ownedPod("a", "1"), ownedPod("b", "2"))
	pd, err := NewPodDeleter(cs, WantNamespace)
	if err != nil {
		t.Fatal(err)
	}
	r.Pods = pd
	sc := testScenario()
	sc.FaultKind = FaultPodDelete
	sc.Baseline, sc.FaultSecs = 1, 2
	term, err := r.Run(context.Background(), sc, "run-pod")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if term != StPassed {
		t.Fatalf("terminal=%s, want PASSED", term)
	}
	// Deleted pod gone; sibling untouched.
	pods, _ := cs.CoreV1().Pods(WantNamespace).List(context.Background(), metav1.ListOptions{})
	names := map[string]bool{}
	for _, p := range pods.Items {
		names[p.Name] = true
	}
	if names["a"] || !names["b"] {
		t.Fatalf("pods=%v, want only b", names)
	}
	row, _ := j.GetRun("run-pod")
	if row.State != StPassed {
		t.Fatalf("journal=%s", row.State)
	}
}

// Runner dep-fault integration (fake controller).
func TestRunnerDepFaultPass(t *testing.T) {
	d := newLabDouble(t, "stable", 0)
	r, _, _ := testRunner(t, d.srv.URL)
	dep := NewFakeDepFault()
	r.Dep = dep
	sc := testScenario()
	sc.FaultKind = FaultDepOutage
	sc.Baseline, sc.FaultSecs = 1, 2
	term, err := r.Run(context.Background(), sc, "run-dep")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if term != StPassed {
		t.Fatalf("terminal=%s, want PASSED", term)
	}
	if dep.SetCalls != 1 || dep.ClearCalls != 1 {
		t.Fatalf("set=%d clear=%d, want 1/1", dep.SetCalls, dep.ClearCalls)
	}
	if bad, _ := dep.IsFailing(context.Background()); bad {
		t.Fatal("fault still active after run")
	}
}

// Unsupported backend is rejected before any journal row or lock.
func TestRunnerRejectsUnconfiguredBackend(t *testing.T) {
	d := newLabDouble(t, "stable", 0)
	r, j, _ := testRunner(t, d.srv.URL)
	sc := testScenario()
	sc.FaultKind = FaultPodDelete // Pods nil
	if _, err := r.Run(context.Background(), sc, "run-nopod"); err == nil {
		t.Fatal("expected backend error")
	}
	if _, err := j.GetRun("run-nopod"); err == nil {
		t.Fatal("journal row created despite rejection")
	}
	sc.FaultKind = FaultDepOutage // Dep nil
	if _, err := r.Run(context.Background(), sc, "run-nodep"); err == nil {
		t.Fatal("expected backend error")
	}
}

// Reconcile of a dangling pod fault verifies restoration.
func TestReconcilePodFault(t *testing.T) {
	d := newLabDouble(t, "stable", 0)
	r, j, _ := testRunner(t, d.srv.URL)
	cs := fake.NewSimpleClientset(labDeployment("x"), labReplicaSet(), ownedPod("b", "2"))
	pd, err := NewPodDeleter(cs, WantNamespace)
	if err != nil {
		t.Fatal(err)
	}
	r.Pods = pd
	// Crash mid-run with a recorded fault whose pod is ALREADY gone.
	if err := j.CreateRun("r-pod", "h", "reservations", 1, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{StPreflight, StBaseline, StInjecting} {
		prev := StCreated
		if s != StPreflight {
			r0, _ := j.GetRun("r-pod")
			prev = r0.State
		}
		if err := j.Transition("r-pod", prev, s, "e", "{}", ""); err != nil {
			t.Fatal(err)
		}
	}
	if err := j.RecordFault("r-pod", "r-pod-f1", FaultPodDelete,
		`{"slot":"stable","deployment":"api-stable","pod":"a","uid":"pod-1"}`, "2030-01-01T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	done, err := r.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(done) != 1 {
		t.Fatalf("reconciled=%v", done)
	}
	row, _ := j.GetRun("r-pod")
	if row.State != StFailed {
		t.Fatalf("state=%s, want FAILED", row.State)
	}
}

// Phase-disjoint keys (contract §5/§7): baseline and fault phases must not
// share idempotency keys, or fault-phase writes become accidental replays.
func TestPhaseSeedOffset(t *testing.T) {
	if got := phaseSeedOffset("baseline"); got != 0 {
		t.Fatalf("baseline offset=%d, want 0 (scenario seed unchanged)", got)
	}
	if got := phaseSeedOffset("fault"); got != 1000000 {
		t.Fatalf("fault offset=%d, want 1000000", got)
	}
	if got := phaseSeedOffset("recovery"); got != 2000000 {
		t.Fatalf("recovery offset=%d, want 2000000", got)
	}
}

func TestPhaseKeyNamespacesDisjoint(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"x"}`))
	}))
	defer s.Close()
	hashes := map[string]string{}
	for _, phase := range []string{"baseline", "fault", "recovery"} {
		var buf strings.Builder
		rn := &loadgen.Runner{BaseURL: s.URL, Out: &writerFunc{fn: func(p []byte) (int, error) {
			return buf.Write(p)
		}}}
		if _, err := rn.Run(context.Background(), loadgen.Config{
			Rate: 20, Duration: 2 * time.Second, Seed: 7 + phaseSeedOffset(phase),
			Timeout: 2 * time.Second,
		}); err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
			var a struct {
				KeyHash string `json:"key_hash"`
			}
			if err := json.Unmarshal([]byte(line), &a); err != nil {
				t.Fatal(err)
			}
			if prev, dup := hashes[a.KeyHash]; dup {
				t.Fatalf("key hash %s shared by %s and %s", a.KeyHash, prev, phase)
			}
			hashes[a.KeyHash] = phase
		}
	}
	if len(hashes) != 120 {
		t.Fatalf("hashes=%d, want 120 (40 per phase, disjoint)", len(hashes))
	}
}

type writerFunc struct {
	fn func([]byte) (int, error)
}

func (w *writerFunc) Write(p []byte) (int, error) { return w.fn(p) }

// ---- Finding D: cleanup status and post-cleanup health are distinct ----

// eventKinds lists one run's journal events in write order.
func eventKinds(t *testing.T, j *Journal, runID string) []string {
	t.Helper()
	rows, err := j.db.Query(`SELECT kind FROM events WHERE run_id = ? ORDER BY rowid`, runID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var kinds []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			t.Fatal(err)
		}
		kinds = append(kinds, k)
	}
	return kinds
}

func hasEvent(kinds []string, want string) bool {
	for _, k := range kinds {
		if k == want {
			return true
		}
	}
	return false
}

// A run whose service still answers 500 after cleanup must not pass: cleanup
// restoration alone is not recovery.
func TestRecoveryUnhealthyFailsRun(t *testing.T) {
	d := newLabDouble(t, "stable", 0)
	prefix := fmt.Sprintf("load-%d-", testScenario().Seed+phaseSeedOffset("recovery"))
	d.breakPrefix.Store(&prefix)
	r, j, f := testRunner(t, d.srv.URL)
	sc := testScenario()

	term, err := r.Run(context.Background(), sc, "run-unhealthy")
	if err == nil {
		t.Fatal("expected an error from an unhealthy recovery phase")
	}
	if term != StFailed {
		t.Fatalf("terminal=%s, want FAILED (never PASSED on unhealthy service)", term)
	}
	if !strings.Contains(err.Error(), "not healthy") {
		t.Fatalf("error=%q, want the post-cleanup health reason", err.Error())
	}
	row, _ := j.GetRun("run-unhealthy")
	if row.State != StFailed {
		t.Fatalf("journal=%s, want FAILED", row.State)
	}
	if live, _ := f.Active(context.Background()); len(live) != 0 {
		t.Fatalf("faults live after run: %v", live)
	}
	kinds := eventKinds(t, j, "run-unhealthy")
	for _, want := range []string{"cleanup-ok", "recovery-health", "recovery-failed", "phase-load"} {
		if !hasEvent(kinds, want) {
			t.Fatalf("event %q missing from %v", want, kinds)
		}
	}
	if hasEvent(kinds, "recovery-ok") {
		t.Fatalf("recovery-ok recorded for an unhealthy run: %v", kinds)
	}
	// The failing recovery phase keeps its evidence.
	raw, rerr := os.ReadFile(filepath.Join(r.OutDir, "phases.json"))
	if rerr != nil {
		t.Fatal(rerr)
	}
	var ph struct {
		Phases []PhaseResult `json:"phases"`
	}
	if err := json.Unmarshal(raw, &ph); err != nil {
		t.Fatal(err)
	}
	var sawRecovery bool
	for _, p := range ph.Phases {
		if p.Phase != "recovery" {
			continue
		}
		sawRecovery = true
		if p.Summary.Completed == 0 || p.Summary.Valid != (p.Summary.InvalidReason == "") {
			t.Fatalf("recovery summary implausible: %+v", p.Summary)
		}
	}
	if !sawRecovery {
		t.Fatalf("phases.json has no recovery phase: %s", raw)
	}
}

// A permissive abort threshold must not leak into recovery: with
// AbortMax=1.0 no failure ratio ever exceeds the abort bound, so the old
// `h.Ratio > sc.AbortMax` check let a 100% failed recovery phase PASS.
// Recovery carries its own budget (MaxRecoveryFailureRatio).
func TestRecoveryAllFailWithPermissiveAbortFails(t *testing.T) {
	d := newLabDouble(t, "stable", 0)
	prefix := fmt.Sprintf("load-%d-", testScenario().Seed+phaseSeedOffset("recovery"))
	d.breakPrefix.Store(&prefix)
	r, j, _ := testRunner(t, d.srv.URL)
	sc := testScenario()
	sc.AbortMax = 1.0 // must-not-abort fault experiments still need a strict recovery

	term, err := r.Run(context.Background(), sc, "run-permissive-abort")
	if err == nil {
		t.Fatal("expected failure: 100% failed recovery must not pass with AbortMax=1.0")
	}
	if term != StFailed {
		t.Fatalf("terminal=%s, want FAILED", term)
	}
	row, _ := j.GetRun("run-permissive-abort")
	if row.State != StFailed {
		t.Fatalf("journal=%s, want FAILED", row.State)
	}
	kinds := eventKinds(t, j, "run-permissive-abort")
	if hasEvent(kinds, "recovery-ok") {
		t.Fatalf("recovery-ok recorded for a 100%% failed recovery: %v", kinds)
	}
	if !hasEvent(kinds, "recovery-failed") {
		t.Fatalf("recovery-failed missing: %v", kinds)
	}
}

// Answered is not recovered: an all-4xx recovery phase has failure ratio 0
// under any threshold, but zero successful (2xx) requests prove nothing
// about the restored service.
func TestRecoveryWithoutSuccessesFails(t *testing.T) {
	d := newLabDouble(t, "stable", 0)
	prefix := fmt.Sprintf("load-%d-", testScenario().Seed+phaseSeedOffset("recovery"))
	d.breakPrefix.Store(&prefix)
	d.breakStatus.Store(http.StatusBadRequest)
	r, j, _ := testRunner(t, d.srv.URL)
	sc := testScenario()

	term, err := r.Run(context.Background(), sc, "run-no-success")
	if err == nil {
		t.Fatal("expected failure: recovery with zero successful requests must not pass")
	}
	if term != StFailed {
		t.Fatalf("terminal=%s, want FAILED", term)
	}
	if !strings.Contains(err.Error(), "not proven") {
		t.Fatalf("error=%q, want the successful-request reason", err.Error())
	}
	row, _ := j.GetRun("run-no-success")
	if row.State != StFailed {
		t.Fatalf("journal=%s, want FAILED", row.State)
	}
}

// Declared assertions without a configured ledger fail closed.
func TestAssertionsWithoutLedgerFailClosed(t *testing.T) {
	d := newLabDouble(t, "stable", 0)
	r, j, _ := testRunner(t, d.srv.URL)
	sc := testScenario()
	sc.AssertsDeclared = true
	sc.AssertDuplicates, sc.AssertNegativeInv = true, true

	term, err := r.Run(context.Background(), sc, "run-no-ledger")
	if err == nil {
		t.Fatal("expected failure when assertions are declared without a ledger")
	}
	if term != StFailed {
		t.Fatalf("terminal=%s, want FAILED", term)
	}
	if !strings.Contains(err.Error(), "no ledger configured") {
		t.Fatalf("error=%q, want fail-closed ledger reason", err.Error())
	}
	kinds := eventKinds(t, j, "run-no-ledger")
	if !hasEvent(kinds, "recovery-failed") {
		t.Fatalf("recovery-failed missing: %v", kinds)
	}
	if hasEvent(kinds, "oracle") {
		t.Fatalf("oracle event written without a ledger: %v", kinds)
	}
}

// Cleanup status and recovery status are recorded separately: an aborted
// observation cleans up but never claims recovery.
func TestAbortRecordsRecoverySkipped(t *testing.T) {
	d := newLabDouble(t, "stable", 1) // every request fails → abort
	r, j, _ := testRunner(t, d.srv.URL)
	sc := testScenario()
	sc.FaultSecs = 5 // abort needs ~3 one-second ticks to see two windows

	term, err := r.Run(context.Background(), sc, "run-abort")
	if err == nil {
		t.Fatal("expected an abort failure")
	}
	if term != StFailed {
		t.Fatalf("terminal=%s, want FAILED", term)
	}
	if !strings.Contains(err.Error(), "abort") {
		t.Fatalf("error=%q, want the abort reason", err.Error())
	}
	kinds := eventKinds(t, j, "run-abort")
	if !hasEvent(kinds, "cleanup-ok") {
		t.Fatalf("cleanup-ok missing after a successful cleanup: %v", kinds)
	}
	if !hasEvent(kinds, "recovery-skipped") {
		t.Fatalf("recovery-skipped missing: %v", kinds)
	}
	if hasEvent(kinds, "recovery-ok") {
		t.Fatalf("recovery-ok recorded for an aborted run: %v", kinds)
	}
}

// ---- Finding E: invalid workloads and persistence failures block PASSED ----

// A phase that delivers nothing is an invalid workload: it must fail the run
// and be persisted as evidence instead of being silently ignored.
func TestInvalidWorkloadNeverPasses(t *testing.T) {
	d := newLabDouble(t, "stable", 0)
	r, j, _ := testRunner(t, d.srv.URL)
	sc := testScenario()
	// Parseable scenario: rate > 0 but 0 planned slots in a 1s phase.
	sc.Rate = 0.5

	term, err := r.Run(context.Background(), sc, "run-invalid")
	if err == nil {
		t.Fatal("expected failure for a workload with no planned slots")
	}
	if term == StPassed {
		t.Fatalf("terminal=PASSED for an invalid workload (error %v)", err)
	}
	if term != StFailed {
		t.Fatalf("terminal=%s, want FAILED", term)
	}
	if !strings.Contains(err.Error(), "invalid workload (baseline)") {
		t.Fatalf("error=%q, want invalid-workload reason", err.Error())
	}
	kinds := eventKinds(t, j, "run-invalid")
	if !hasEvent(kinds, "phase-load") {
		t.Fatalf("phase-load evidence event missing: %v", kinds)
	}
	if !hasEvent(kinds, "recovery-skipped") {
		t.Fatalf("recovery-skipped missing: %v", kinds)
	}
	raw, rerr := os.ReadFile(filepath.Join(r.OutDir, "phases.json"))
	if rerr != nil {
		t.Fatal(rerr)
	}
	var ph struct {
		Phases []PhaseResult `json:"phases"`
	}
	if err := json.Unmarshal(raw, &ph); err != nil {
		t.Fatal(err)
	}
	if len(ph.Phases) == 0 {
		t.Fatalf("no phase evidence written: %s", raw)
	}
	if ph.Phases[0].Phase != "baseline" || ph.Phases[0].Summary.Valid {
		t.Fatalf("invalid baseline not preserved: %+v", ph.Phases[0])
	}
	if ph.Phases[0].Summary.InvalidReason == "" {
		t.Fatalf("missing invalid reason: %+v", ph.Phases[0])
	}
}

// Failing to persist phase evidence fails the run: evidence loss is never a
// warning.
func TestEvidenceWriteFailureFailsRun(t *testing.T) {
	d := newLabDouble(t, "stable", 0)
	r, j, _ := testRunner(t, d.srv.URL)
	blocker := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	r.OutDir = blocker
	sc := testScenario()

	term, err := r.Run(context.Background(), sc, "run-evidence")
	if err == nil {
		t.Fatal("expected failure when phase evidence cannot be written")
	}
	if term == StPassed {
		t.Fatalf("terminal=PASSED despite failed evidence write (error %v)", err)
	}
	if !strings.Contains(err.Error(), "phases.json") {
		t.Fatalf("error=%q, want the evidence-write failure", err.Error())
	}
	row, _ := j.GetRun("run-evidence")
	if row.State == StPassed {
		t.Fatalf("journal state PASSED after evidence failure: %s", row.State)
	}
}

// A journal write failure in the phase-persist path propagates instead of
// being swallowed.
func TestPhasePersistJournalErrorPropagates(t *testing.T) {
	r, j, _ := testRunner(t, "http://127.0.0.1:1")
	if _, err := j.db.Exec(`DROP TABLE events`); err != nil {
		t.Fatal(err)
	}
	err := r.recordPhase("run-x", "baseline", loadgen.Summary{Valid: false, InvalidReason: "simulated"})
	if err == nil {
		t.Fatal("journal failure was swallowed")
	}
	if !strings.Contains(err.Error(), "phase-load") {
		t.Fatalf("error=%q, want the phase-load journal failure", err.Error())
	}
}

// Recovery keys are a third namespace: baseline, fault and recovery never
// re-offer each other's idempotency keys.
func TestRecoverySeedOffsetDisjoint(t *testing.T) {
	if phaseSeedOffset("recovery") == phaseSeedOffset("baseline") ||
		phaseSeedOffset("recovery") == phaseSeedOffset("fault") {
		t.Fatalf("recovery shares a seed namespace: baseline=%d fault=%d recovery=%d",
			phaseSeedOffset("baseline"), phaseSeedOffset("fault"), phaseSeedOffset("recovery"))
	}
}
