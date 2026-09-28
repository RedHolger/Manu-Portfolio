# HANDOFF — sre-portfolio (BudgetGuard → FaultLab)

## Baselines (do not rewrite)
- `0fe8b77` (master): BudgetGuard scoped completion.
- `cad2f7e` (correctness-fixes): review-findings fixes + full-window acceptance.
- `faultlab-dev` (this branch, from `cad2f7e`): FaultLab F1–F2 work.

## BudgetGuard status: implemented, self-verified, EXTERNAL REVIEW PENDING
- Unit/integration: `go test ./...` green incl. `-race`; promtool 10/10.
- Native 15/15 preliminary + kind full-window 9/9 (healthy/error/slow) +
  client-error PASS + Grafana screenshots. Contaminated runs preserved.
- Pending (not completed): independent external re-review; remaining
  repetitions toward 10-per-class (healthy 10/10 done; error/slow need
  kind top-ups beyond the full-window 3-seed sets).

## FaultLab scope (this branch): F1 + F2 only
- F1: strict scenario validation, `plan`, durable SQLite journal, state
  machine, fake clock, fake injector + tests. No cluster needed.
- F2: real gateway delay/failure injection, bounded TTL, workload
  observation, abort, cleanup, restart reconciliation + one bounded live
  demo per acceptance case (7 cases), subject to disk floor + watchdog.
- Deferred: F3/F4, pod deletion, full correctness oracle, RecoverOps.

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
