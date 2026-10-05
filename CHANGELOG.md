# Unreleased source fixes — 2026-10-05 (recorded-evidence review follow-ups)

BudgetGuard coverage gap measured between consecutive present buckets
((worst+1)*step: one empty 15s bucket = 30s inter-sample gap > 20s limit);
FaultLab recovery decoupled from the abort threshold (independent 5%
`MaxRecoveryFailureRatio` + ≥3 successful 2xx requests, so `AbortMax=1.0`
and all-4xx recoveries can no longer pass). Regression tests fail on the
prior code and pass on the fix; `go test ./...` green; no live lab rerun
(docker down), so IMPLEMENTED_UNVERIFIED against the lab.

# Unreleased source fixes — 2026-10-03

Durable UID-bound execution intent, execution-time budgets, conditional template
patches, autonomous server-side recovery proof, restart/cancellation safety;
measured paired driver, sequential demo, image/setup/registration tools; loadgen
key isolation/write errors and stricter context/disk guards. See
`docs/CORRECTED_HANDOFF.md`. Live acceptance pending; no release tag.

# Unreleased source fixes — 2026-10-05 (findings A–E)

Loadgen absolute schedule + delivery contract and bounded drain; BudgetGuard
source-sample coverage gate and replay-fixture validation; FaultLab
post-cleanup recovery health gate, per-phase workload evidence, fail-closed
`--pg-dsn` assertions, and propagated persistence errors. Unit-verified
(`go test ./...`, `-race` on `internal/faultlab`); no live run yet, so these
are IMPLEMENTED_UNVERIFIED against the lab.

# CHANGELOG.md — delivered behavior and known limitations per release.

Unreleased v1.0 work is described as pending with its gate; nothing below
claims acceptance before RELEASE_STATUS.md says ACCEPTED.

## Shared lab (v1.0 pending readiness re-check)
- Delivered: Go reservation API (PostgreSQL + MemStore), transactional
  reservations, idempotency keys (same-key/same-payload replay 200+same ID,
  conflicts 409), gateway stable/candidate routing with version CAS,
  metrics, TTL fault admin, load generator with JSONL history, kind
  deployment with PostgreSQL/Prometheus/Grafana.
- Known limitations: single-node kind models app/pod failure only (D-004);
  gateway upstream transport timeout hardcoded 5s (no server profile yet).

## BudgetGuard (v1.0 pending B-02 tail, B-03 top-ups, B-04 docs)
- Delivered: strict SLO parsing, SLI/error-budget math, Prometheus
  rule compiler (FastBurn/page + TicketBurn/ticket per SLI), evaluator
  PASS=0/FAIL=2/INCONCLUSIVE=3, coverage gate (≤10% missing, no gap >20s),
  replay fixtures. Corrections BG-01–BG-10 at `cad2f7e` (see TASKS.md).
- Known limitations: 5-minute canary never proves 30-day SLO; legacy
  80-second runs are preliminary short-window evidence, not acceptance.

## FaultLab (v1.0 pending F-03 audit, F-04/F-05 close-out, phase-key fix)
- Delivered: strict scenario schema, SQLite journal (intent-before-mutation,
  CAS transitions, service lock, reconcile), phased runner (gateway/pod/
  dependency backends, abort, fresh-context cleanup, CLEANUP_FAILED),
  UID-precondition pod deletion, token-gated dependency faults, reservation
  oracle (conservation, key uniqueness, ambiguous reconciliation), CLI
  (validate/plan/run/status/cleanup/reconcile/report + diagnostics),
  baseline-vs-resilient comparison ("client retry resilience": no retry vs
  one same-key retry; retry faults independently sampled per attempt).
- Known limitations: load JSONL preserves final attempt outcomes only
  (attempts = lines + retried lines; first-attempt latency unavailable);
  runner phases currently share one keyspace when the seed is constant
  (fix required by contract §7 — in progress).

## RecoverOps (v1.0: R1-R3 accepted, R4 implemented-unverified)
- R1 delivered: Alertmanager-compatible webhook (auth, 1MiB cap, strict
  validation, commit-before-202, redelivery dedupe, order-safe terminal
  handling), durable SQLite state (incidents, alert occurrences, actions,
  known-good templates, monotonic events, persisted mode), CLI
  (serve/policy-validate/incident-show/replay/register-good/mode).
  No cluster mutation by construction (dependency-tested).
- R2 delivered: policy evaluation (target pins, 15m evidence freshness,
  snapshot presence + target binding, cooldown/hourly limits from durable
  rows surviving restart), OBSERVED/PROPOSED proposal records
  (observe vs enforce-lab), incident-scoped single-action key, actions in
  incident show. Live already-known-good comparison deferred to R3.
- R3 delivered and accepted live: UID-pinned template-only rollback,
  restart reconciliation, duplicate-delivery idempotency, and least-
  privilege lab deployment.
- R4 delivered but not yet accepted: genuine Alertmanager path,
  reconciler, cooldown/budget refusals, and persisted verification with
  server-measured Prometheus windows. The 10-pair matrix and portfolio
  integration demo remain incomplete.

# Unreleased demo site + CI — 2026-10-05

Static demo site (`site/`, three routes) generated from the Go CLI and
committed `results/` bundles by `scripts/build-demo-site.sh` with a `--check`
drift gate wired into CI (`.github/workflows/ci.yml`, unrun). Published to
https://sre-portfolio-demos.vercel.app/ via `scripts/deploy-vercel.sh`, which
stamps the deployed commit into `BUILD.json` and refuses to publish on
mismatch; verification evidence in `results/deploy-20261005T064754Z/`.
`PUBLIC_LINKS.md` and `CV_SNIPPETS.tex` carry only URLs fetched anonymously
on 2026-10-05. Published to github.com/RedHolger/Manu-Portfolio with CI green
(go/tests, promtool, shell self-tests, site drift); six shell scripts that
were tracked 100644 under `core.fileMode=false` are now 100755, so
`make setup`/`register` and the demo generator work in a fresh clone.
No lab behaviour changed; no live cluster run.
