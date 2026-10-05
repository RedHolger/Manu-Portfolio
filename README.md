# sre-portfolio — a shared SRE lab and three v1.0 products

**BudgetGuard** (SLO compiler + canary gate), **FaultLab** (journaled chaos
experiments), **RecoverOps** (evidence-bound automated recovery) — built on a
local `kind` lab with a reservation API, versioned gateway, load generator,
PostgreSQL and Prometheus.

Status: work is tracked in [RELEASE_STATUS.md](RELEASE_STATUS.md) (one gate
table) against the frozen contract in [RELEASE_PLAN.md](RELEASE_PLAN.md).
BudgetGuard, FaultLab and lab readiness are **ACCEPTED**; RecoverOps R4 and the
full portfolio integration are **IMPLEMENTED_UNVERIFIED**. Nothing on this page
claims more than that table does.

## Products

| Product | What it does | Status |
|---|---|---|
| [BudgetGuard](docs/projects/budgetguard.md) | Compiles a written SLO into Prometheus alert rules and turns measured traffic into PASS / FAIL / **INCONCLUSIVE** release decisions (exit 0 / 2 / 3), with a full-window telemetry coverage gate | B-01…B-04 ACCEPTED |
| [FaultLab](docs/projects/faultlab.md) | Runs bounded fault experiments (delay, connection failure, dependency failure, pod delete) against the lab, journaled so the run can be audited afterwards: intent → apply → observe → cleanup → recovery health | F-01…F-05 ACCEPTED |
| [RecoverOps](docs/projects/recoverops.md) | Accepts alert webhooks, proposes a policy-checked single remediation, executes it as a UID-pinned template-only patch in the lab, and verifies recovery from measured traffic before RESOLVED | R1…R3 ACCEPTED, R4 unverified |

Shared lab: `cmd/labapi` (idempotent reservation API, PostgreSQL + MemStore),
`cmd/labgateway` (versioned routing + TTL'd fault admin),
`cmd/labload` (open-loop load generator with a JSONL history and validity
checks), `deploy/` (kind cluster, Prometheus, Grafana).

## Quick start

```sh
go build ./...              # or: go build -o bin/ ./cmd/...
go vet ./... && gofmt -l cmd internal
go test ./...
make test-rules             # promtool rule fixtures (requires promtool)
make test-scripts           # shell self-tests
make lint                   # gofmt + vet + config validation
```

Full lab path (Docker + kind required; `make doctor` reports missing
prerequisites, it does not pretend they are present):

```sh
make doctor         # diagnostics only; nonzero = prerequisite missing
make bootstrap      # creates ONLY the kind-sre-lab cluster
./scripts/build-images.sh
kubectl apply -k deploy/base        # + secrets + rules, see docs/runbook.md
make lab-up && make smoke
```

Every Kubernetes-touching script and binary requires the explicit
`kind-sre-lab` context; there is no fallback to whatever context is current.

## Evidence

- Gate table: [RELEASE_STATUS.md](RELEASE_STATUS.md) — commit/evidence per gate,
  including what is still unverified.
- Result index with per-environment counts:
  [results/SUMMARY.md](results/SUMMARY.md); FaultLab demos:
  [results/faultlab/EVIDENCE.md](results/faultlab/EVIDENCE.md).
- Failed, inconclusive and contaminated runs are preserved with reasons and
  excluded from benchmark totals — they are never deleted to make a suite
  green.
- Corrections from independent review (BG-01…BG-10) are recorded in
  [TASKS.md](TASKS.md); full review text in
  [docs/REVIEW.md](docs/REVIEW.md).

## Safety and scope

- This is a **local teaching lab**: single-node kind models application and pod
  failure, not production networking or multi-cluster failure.
- No public Kubernetes control plane, no public fault-injection admin, no
  execute/patch/rollback action reachable by anonymous users. Admin tokens
  (`LAB_ADMIN_TOKEN`, `RECOVEROPS_TOKEN`) are read from the environment or an
  ignored `*.env` file and are never committed.
- Faults are TTL'd and cleared by the gateway itself; cleanup runs on a fresh
  bounded context and is idempotent; RecoverOps defaults to `observe` mode,
  which performs zero cluster writes.
- A 5-minute canary never proves a 30-day SLO. Every short-window run is
  labelled as preliminary evidence.

## Known imperfections

- Grafana bad-ratio panels autoscale % axes coarsely (data is correct,
  presentation is unpolished).
- Contaminated runs exist and are listed in
  [results/SUMMARY.md](results/SUMMARY.md) with reasons.
- RecoverOps R4 and the portfolio integration are not yet live-verified; see
  the gate table for exactly what remains.

## Repository layout

```
cmd/         labapi, labgateway, labload, budgetguard, faultlab, recoverops
internal/    workload, gateway, telemetry, budgetguard, faultlab, recoverops
configs/     SLO and fault-scenario YAML (the reviewed source of truth)
monitoring/  generated alert rules + promtool fixtures
deploy/      kustomize base for the kind lab (API, gateway, DB, Prometheus)
scripts/     bootstrap, suites, acceptance, safety (disk floor, context guard)
results/     run bundles: journals, decisions, reports, screenshots
docs/        architecture, runbook, review, limitations, postmortems, plans
```
