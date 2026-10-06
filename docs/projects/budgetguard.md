# BudgetGuard — SLO compiler and canary release gate

**Status:** gates B-01 … B-04 **ACCEPTED** (see [RELEASE_STATUS.md](../../RELEASE_STATUS.md)).
Unit tests, `promtool` rule tests, native runs and kind (`sre-lab`) runs are the
evidence; nothing here claims a 30-day SLO.

## What it is

A Go CLI that turns a written SLO into executable control: Prometheus alerting
rules and a pass/fail release decision from measured traffic.

| Command | Purpose |
|---|---|
| `budgetguard validate --config F` | Strict SLO parsing (bounds, units, required fields) |
| `budgetguard compile --config F --out monitoring/generated-rules.yaml` | Emit alert rules (FastBurn/page + TicketBurn/ticket per SLI) |
| `budgetguard status --config F --prometheus URL` | Current error-budget state |
| `budgetguard evaluate --config F --start … --end … --out FILE` | Window decision: exit **0** PASS, **2** FAIL, **3** INCONCLUSIVE |
| `budgetguard replay --fixture FILE --config F --end … --out FILE` | Deterministic offline decision from a recorded fixture |

Config: [`configs/slos/reservations.yaml`](../../configs/slos/reservations.yaml).
Generated rules: `monitoring/generated-rules.yaml` (tests:
`monitoring/rule-tests.yaml`, `make test-rules`).

## Why it exists

A release gate that cannot say "I don't know" will eventually ship on bad data.
BudgetGuard separates three outcomes on purpose:

- **PASS** — thresholds met *and* the window is trustworthy.
- **FAIL** — an SLO/gate threshold was breached.
- **INCONCLUSIVE** — telemetry missing, stale, or traffic below the minimum:
  a decision is refused rather than guessed.

## Correctness properties enforced in code

- **Coverage gate (full window).** The evaluated window must be covered by
  real source samples: at most 10% missing buckets per slot, computed with
  `count_over_time` over 15 s buckets from the two required series per slot
  (`lab_requests_total` and the latency histogram bucket at the
  SLO threshold). The 20 s gap limit is enforced on actual consecutive
  source timestamps (`max(timestamp(...))` per step), not bucket occupancy:
  a 30 s scrape gap straddling the grid, or samples hugging opposite bucket
  edges, cannot hide inside occupied buckets. Sparse or unevenly scraped
  telemetry cannot masquerade as coverage.
- **Telemetry errors keep the window.** A failed query is recorded as a
  telemetry error on the decision and the window start/end are preserved, so
  the evidence of *what* was missing survives.
- **Fractional counts.** Fixture and query counts keep their fractions;
  only count-like quantities are rounded, and only where the contract says so.
- **Fixture validation.** A replay fixture must have every required series,
  finite values, and consistent relationships (non-negative counts,
  `bad ≤ total`, threshold bucket ≤ le+Inf). Malformed fixtures are rejected
  instead of replayed into a green result.
- **Thresholds are configured, not hard-coded** (`configs/slos/reservations.yaml`).

## Reproduce

```sh
go test ./...                          # unit
make test-rules                        # promtool fixtures (10/10)
bin/budgetguard validate --config configs/slos/reservations.yaml
bin/budgetguard replay \
  --fixture tests/fixtures/releases/healthy.json \
  --config configs/slos/reservations.yaml \
  --end 2026-10-03T05:00:00Z --out /tmp/replay.json; echo $?   # 0
bin/budgetguard evaluate --config configs/slos/reservations.yaml \
  --start … --end … --out /tmp/decision.json; echo $?
```

Live kind path (needs the lab, see the [root README](../../README.md)):
`make lab-up && ./scripts/seeded-suite-kind.sh`.

## Evidence

- Full-window acceptance: `results/budgetguard/kind-fullwindow-20261003T025057Z/`
  (10 healthy / 10 error / 10 slow, zero INCONCLUSIVE) and
  `kind-fullwindow-20260928T011047Z/`.
- Telemetry loss → INCONCLUSIVE with saved query evidence:
  `results/budgetguard/telemetry-loss-20261003T024957Z/decision.json`.
- Fixed live demo (PASS/FAIL/exits on the wire):
  `results/budgetguard/20261003T045748Z/` (+ `exits.log`).
- Index and per-environment counts: [`results/SUMMARY.md`](../../results/SUMMARY.md).

## Known limitations

- A 5-minute canary never proves a 30-day SLO: `history_complete` is always
  false in the lab, and every run is labelled as such.
- Runs before 2026-09-28 predate the coverage gate; they are retained as
  preliminary short-window evidence and are excluded from full-window counts.
- Contaminated and below-minimum-traffic runs are preserved (with reasons),
  never deleted, and excluded from benchmark totals.
- Grafana bad-ratio panels autoscale % axes coarsely — the data is right, the
  axis presentation is unpolished.
