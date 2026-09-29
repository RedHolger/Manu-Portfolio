# HANDOFF — sre-portfolio (BudgetGuard → FaultLab)

## Baselines (do not rewrite)
- `0fe8b77` (master): BudgetGuard scoped completion.
- `cad2f7e` (correctness-fixes): review-findings fixes + full-window acceptance.
- `26e2fe3` (faultlab-dev): FaultLab F1 + F2 complete, 7/7 demos evidenced.
- This session continues on `faultlab-dev`: F3 + F4 offline only.

## BudgetGuard status: implemented, self-verified, EXTERNAL REVIEW PENDING
- Unit/integration: `go test ./...` green incl. `-race`; promtool 10/10.
- Native 15/15 preliminary + kind full-window 9/9 (healthy/error/slow) +
  client-error PASS + Grafana screenshots. Contaminated runs preserved.
- Pending (not completed): independent external re-review; remaining
  repetitions toward 10-per-class (healthy 10/10 done; error/slow need
  kind top-ups beyond the full-window 3-seed sets).

## FaultLab scope: F1 + F2 done; F3 + F4 offline in progress
- F1/F2 evidence: `results/faultlab/` (journals, decisions, EVIDENCE.md).
- F3/F4 run fully offline (1.5–5GiB free; no image builds, no live runs,
  no deletions). Real Kubernetes/PostgreSQL acceptance: UNEXECUTED.
- Deferred intact: RecoverOps, repeated benchmarks.

## Test success vs experiment outcome (read before citing)
- A passing unit/integration test proves CODE behavior (parser rejects,
  journal transitions, fake lifecycles, fixture matrices).
- An experiment outcome (PASS/FAIL/INCONCLUSIVE, burn rates, recovery
  times) is evidence ONLY from executed live runs with saved raw artifacts.
- Telemetry-loss demo "passing" means FaultLab correctly reported
  `CLEANUP_FAILED` — it does NOT establish that restoration succeeded.
- Contaminated/excluded runs stay excluded however green the unit suite is.

## Interfaces to reuse (verify before integrating)
- Gateway admin: `PUT /admin/faults/{id}` (TTL ≤120s), `DELETE` idempotent,
  `GET /admin/state`, `PUT /admin/routing` (CAS version) — see
  `cmd/labgateway/main.go`, `internal/gateway/gateway.go`.
- Metrics: `lab_requests_total{service,slot,route,result}` + histogram with
  0.3s bucket — `internal/gateway/exp.go`.
- Loadgen: `Runner` (open-loop, JSONL) — `internal/loadgen/loadgen.go`.
- Guards: `scripts/require-context.sh` (kind-sre-lab),
  `scripts/disk-floor.sh`, `scripts/disk-watchdog.sh`.
- Clock: `internal/clock/` (real + fake; check what exists first).

## Environment notes
- kind cluster `sre-lab` exists, deployments scaled to 0; Docker idle.
- Disk is the binding constraint (~2.9GiB at branch time); floor 2GB armed.
- Go 1.27.1, kind 0.33.0, kubectl 1.37.1, promtool 3.15.0, pgx v5.11.0.
- SQLite driver not yet vendored (need CGO-free pure-Go module).

## FaultLab F2 status (faultlab-dev): implemented + demonstrated
Runner, injector, observer, CLI complete; 7/7 acceptance demos evidenced in
results/faultlab/. BudgetGuard external review + 10-per-class: still PENDING.
Next: F3 (pod deletion + dependency faults) only when resourced.
