# FaultLab — chaos experiments you can audit after the fact

**Status:** gates F-01 … F-05 **ACCEPTED** (see
[RELEASE_STATUS.md](../../RELEASE_STATUS.md)). Live demos and paired A/B runs
on kind `sre-lab` are the evidence.

## What it is

A runner for bounded, journaled fault experiments against the lab gateway:
it applies a declared fault for a declared time, watches the workload, and
lands in a state that a third party can audit from the journal alone.

```sh
bin/faultlab validate --scenario configs/faults/delay.yaml
bin/faultlab plan     --scenario configs/faults/delay.yaml
bin/faultlab run      --scenario configs/faults/delay.yaml --out DIR \
                      [--kubeconfig PATH] [--api-admin URL] [--pg-dsn DSN]
bin/faultlab status|report --run-id ID [--db FILE]
bin/faultlab cleanup  --run-id ID   bin/faultlab reconcile
```

Fault kinds: `gateway_delay`, `conn_fail`, `dependency_failure`, `pod_delete`.
Scenarios are YAML (`configs/faults/*.yaml`): workload rate/seed/timeout,
baseline → fault → recovery phases, abort policy, and an optional
`spec.assertions.*` block.

## Safety boundaries

- One experiment per service: the journal takes a lock **before** any mutation;
  a locked or duplicate run row means nothing was touched.
- Intent is journaled before the mutation, applied/cleared after it.
- Explicit `kind-sre-lab` context is required for anything that touches
  Kubernetes (scripts and binaries).
- Faults are TTL'd and cleared by the gateway itself, so a dead runner cannot
  strand a fault; cleanup runs on a fresh bounded context, never the cancelled
  one, and is idempotent.
- Admin access is a bearer token read from `LAB_ADMIN_TOKEN` (never committed);
  the admin service is cluster-internal. There is no public admin endpoint,
  no execute/patch/rollback action exposed to anonymous users.

## The states a reviewer actually reads

`CREATED → PREFLIGHT → BASELINE → INJECTING → OBSERVING → CLEANING → VERIFYING
→ PASSED | FAILED`, with `CLEANUP_FAILED` as the never-a-success branch.

- **CLEANING** proves restoration (recorded faults cleared, zero live faults).
- **VERIFYING** proves the *service* recovered. These are separate facts and
  are journaled separately: `cleanup-ok` is written whenever cleanup restored
  the fault surface, then either `recovery-health` + `recovery-ok`,
  `recovery-health` + `recovery-failed`, or `recovery-skipped` (when the
  observation ended early). A run never reports PASSED on cleanup alone.
- `CLEANUP_FAILED` means restoration could not be verified — it is never
  converted into a pass.

## Correctness properties enforced in code

- **Recovery health gate.** After cleanup the runner executes the configured
  recovery phase and requires at least `MinRecoverySamples` (3) post-cleanup
  requests with a failure ratio within `MaxRecoveryFailureRatio` (5% — an
  independent recovery budget, never the scenario's abort threshold, so a
  permissive `maxFailureRatio: 1.0` cannot let a failed recovery pass) and
  at least `MinRecoverySamples` successful (2xx) requests (`5xx`, timeouts,
  transport errors and no-response count as failures), all within
  `recoveryDeadlineSeconds` (which must strictly exceed
  `recoverySeconds`).
- **Every phase is evidence.** Each workload phase persists a `PhaseResult`
  (journal event `phase-load` and `OutDir/phases.json`): planned/offered/
  launched/completed, schedule vs drain seconds, achieved RPS, validity.
  An invalid or truncated workload fails the run; a phase that delivered
  nothing can never be part of a PASSED run.
- **Persistence errors propagate.** If the journal or the evidence file cannot
  be written, the run fails — evidence loss is not a warning.
- **Declared assertions fail closed.** With `spec.assertions.*` present the
  run requires `--pg-dsn`; the correctness oracle checks the reservation ledger
  (stock conservation, no negative stock, one row per idempotency key,
  acknowledged operations resolving to a matching reservation) and any
  violation fails the run. Without a ledger the run refuses before it starts.
- **Phase-disjoint keys.** Baseline, fault and recovery use disjoint
  idempotency-key namespaces (`phaseSeedOffset` 0 / 1000000 / 2000000), so no
  phase re-offers another phase's keys as accidental replays.
- **Abort is bounded.** Sustained failure ratio above the scenario threshold
  over N consecutive windows triggers cleanup and FAILED, not an unbounded run.

## Reproduce

```sh
go test ./internal/faultlab/            # includes -race
promtool …                              # shared lab rules
make lab-up                             # kind-sre-lab, see root README
LAB_ADMIN_TOKEN=… bin/faultlab run --scenario configs/faults/delay.yaml \
  --out results/faultlab/<your-run>
```

## Evidence

- Seven live demos with journals and `result.json`:
  `results/faultlab/EVIDENCE.md`, `demo2-expire`, `demo3-sigterm`,
  `demo4-sigkill`, `demo6-abort`, `demo6-telemetry`, `demo1-invalid`.
- Ten paired A/B runs (20 runs, alternating order, per-run reseed,
  20/20 oracles CLEAN): `results/faultlab/compare-20260930T*/`.
- Acceptance bundles: `results/faultlab/acceptance-20260929T143244Z/`,
  `runner-acceptance-20260929T221906Z/`.

## Known limitations

- The lab models one gateway and one PostgreSQL-backed service: it is a
  fault-injection teaching rig, not a production control plane.
- The correctness oracle reads the ledger for one SKU within a run; it is not
  a general linearizability checker.
- The newest behaviour (recovery health gate, per-phase evidence,
  fail-closed assertions) is unit-tested with `-race` green; a live end-to-end
  rerun after those changes is still pending, so those paths are
  **IMPLEMENTED_UNVERIFIED** against the cluster.
