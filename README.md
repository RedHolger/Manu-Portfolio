# sre-portfolio — shared SRE lab + BudgetGuard + FaultLab (RecoverOps authorized)

Status: **BudgetGuard v1.0 and FaultLab v1.0 in acceptance; RecoverOps v1.0
authorized** (release authority: `RELEASE_PLAN.md`, gates: `RELEASE_STATUS.md`).
See `results/SUMMARY.md` for exact per-environment counts.

## What this is
- `cmd/labapi` — reservation API (PostgreSQL-backed idempotent writes;
  MemStore for dependency-free local runs)
- `cmd/labgateway` — versioned %-routing + bounded, TTL'd fault admin
- `cmd/labload` — open-loop load generator (JSONL history + validity checks)
- `cmd/budgetguard` — `validate|compile|status|evaluate|replay`:
  SLO compiler (Prometheus rules) + canary release evaluator
  (PASS exit 0 / FAIL exit 2 / INCONCLUSIVE exit 3)

## Verified results (evidence, not claims)
- Unit: `go test ./...` all green incl. `-race`; `go vet` clean.
- Rules: `promtool test rules monitoring/rule-tests.yaml` 9/9
  (healthy, sustained error, moderate/severe burst, slow-only, counter
  reset, client_error exclusion, flat-traffic, missing series).
- Native live: healthy→PASS, error→FAIL, slow→FAIL, empty/stale/dead
  telemetry→INCONCLUSIVE. Seeded suite 15/15 (`results/budgetguard/seeded-…`).
- Kind (`sre-lab`) live: smoke PASS, Postgres integration 4/4, seeded
  healthy 5/5 PASS, error reps FAIL, slow reps FAIL, client-error
  exclusion PASS with 594×400s on the wire (`results/budgetguard/kind-*`).
- Dashboard: `results/budgetguard/screenshots/slo-overview.png` (real
  Grafana, live traffic; axis cosmetics noted below).

## Reproduce (kind path)
```sh
make doctor            # diagnostics only; nonzero = missing prerequisite
make bootstrap         # creates ONLY the kind-sre-lab cluster
./scripts/build-images.sh
kubectl apply -k deploy/base   # + secrets + prometheus-rules ConfigMap, see docs/runbook.md
make lab-up && make smoke
./scripts/seeded-suite-kind.sh # per-rep isolation + disk floor/watchdog
```

## Known imperfections (read before citing numbers)
- Grafana bad-ratio panels autoscale % axes coarsely; legend shows raw
  series names. Data is correct; presentation is unpolished.
- `excludeResults: [client_error]` verified live (400s excluded, PASS held).
- One native slow single + one kind error rep (`error-222`) are marked
  CONTAMINATED (overlapping window / disk-stall crawl) and excluded from
  benchmark counts; both preserved. Details: `docs/limitations.md`,
  `results/SUMMARY.md`, `docs/postmortems/`.
- A 5-minute canary never proves a 30-day SLO: `history_complete` is
  always false in the lab.
