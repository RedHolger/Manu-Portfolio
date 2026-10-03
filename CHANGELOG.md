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

## RecoverOps (v1.0: R1 accepted, R2-R4 TODO)
- R1 delivered: Alertmanager-compatible webhook (auth, 1MiB cap, strict
  validation, commit-before-202, redelivery dedupe, order-safe terminal
  handling), durable SQLite state (incidents, alert occurrences, actions,
  known-good templates, monotonic events, persisted mode), CLI
  (serve/policy-validate/incident-show/replay/register-good/mode).
  No cluster mutation by construction (dependency-tested).
- Known limitations: policy evaluation, observe/enforce behavior, rollback
  execution and recovery verification are R2-R4 work; Alertmanager not yet
  deployed to the lab (R4 prerequisite).
