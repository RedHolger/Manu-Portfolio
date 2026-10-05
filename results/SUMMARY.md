# Results summary — native vs kind, reported separately

> **Window-status labels (added on the correctness-fix branch):** all runs
> through 2026-09-27 are **preliminary short-window evidence** (80–150s of
> traffic evaluated over a 300s window — the coverage gate did not exist).
> `results/budgetguard/kind-fullwindow-20260928T011047Z/` holds the first
> **full-window acceptance evidence** (330s runs, coverage gate enforced).

Contaminated runs (excluded from all benchmark counts, preserved as evidence):
- kind `error-222.*`: 35-minute disk-stall crawl, killed load, INCONCLUSIVE.
- native slow single: overlapping 300s window with prior error run (correct
  verdict, debugging value only).
- kind `error-444.*`: incomplete (halted mid-run, no decision).

Below-minimum-traffic runs (gate working as designed, preserved):
- kind `kind-clienterror/run.*` (seed 4243): 10% invalid traffic correctly
  excluded (bad=0), but candidate eligible 980 < 1000 → INCONCLUSIVE
  (`insufficient_traffic`). Rerun at higher rate (seed 4244) → PASS.

"Healthy 10/10" COMBINES two independent groups: 5 native + 5 kind (below).
No other class is combined; per-environment counts are explicit.

## Group A — native, 15/15 (preliminary, retained)
`results/budgetguard/seeded-20260927T195421Z/` · local processes, MemStore-backed
APIs, local Prometheus. `labload run --rate 70 --duration 80s`, 20% candidate.

| actual | PASS | FAIL | INCONCLUSIVE |
|---|---|---|---|
| healthy (seeds 11,22,33,44,55) | 5 | — | — |
| error (5% seeded) | — | 5 | — |
| slow (+400ms) | — | 5 | — |

Candidate eligible per rep: 1205–4160 (gate minimum 1000).

## Group B — kind (`sre-lab`), 6 clean + 1 caveat + 2 excluded
`results/budgetguard/kind-20260927T204050Z/` · kind v1.37.0, PostgresStore-backed
APIs (postgres:16), in-kind Prometheus, per-rep PG reseed + gateway/prometheus
restart. Same load shape as Group A.

| actual | PASS | FAIL | INCONCLUSIVE / excluded |
|---|---|---|---|
| healthy (seeds 111–555) | 5 (cand 1058–1137) | — | — |
| error seed 111 | — | 1 (cand 1105/55 = 5.0%) | — |
| error seed 333 | — | 1 caveat (98.6% complete, 1017/56) | — |
| error seed 222 | — | — | EXCLUDED (contaminated, see above) |
| error seed 444 | — | — | EXCLUDED (incomplete, halted) |

## Group C — kind full-window acceptance (NEW, correctness-fix branch)
`results/budgetguard/kind-fullwindow-20260928T011047Z/` · rate 25 × 330s,
per-rep isolation, coverage gate enforced, watchdog armed (never tripped).

| actual | PASS | FAIL | INCONCLUSIVE |
|---|---|---|---|
| healthy (1001–1003) | 3 (cand 1446–1612) | — | — |
| error (1001–1003) | — | 3 (cand ≈5% bad) | — |
| slow (1001–1003) | — | 3 (100% slow, 0 errors) | — |

## Class totals toward 10/class (FULL-WINDOW ONLY — v1 acceptance)
Per RELEASE_PLAN.md B-03, legacy 80-second runs do not count and native/kind
groups are not mixed. Full-window = rate 25 × 330s, coverage gate enforced,
per-rep PG reseed + gateway/prometheus restart, kind `sre-lab`.
- `results/budgetguard/kind-fullwindow-20260928T011047Z/` (seeds 1001–1003):
  healthy 3 PASS, error 3 FAIL, slow 3 FAIL, zero INCONCLUSIVE.
- `results/budgetguard/kind-fullwindow-20261003T025057Z/` (seeds 1004–1010):
  healthy 7 PASS (cand 1446–1565, bad 0), error 7 FAIL (cand ≈5% bad),
  slow 7 FAIL (100% slow_or_bad, 0 errors — pure slow signal),
  zero INCONCLUSIVE, no STOP, no failures.
- Totals: healthy **10/10** ✓, error **10/10** ✓, slow **10/10** ✓.
- Stricter-gap confirmation (2026-10-05, not counted above):
  `results/budgetguard/kind-fullwindow-20261005T100739Z/healthy-2001`
  PASS `within_gate` over the 300s window with the inter-sample gap rule
  ((worst+1)*step): real 5s-scrape data shows zero empty 15s buckets.
- Legacy groups A (native 15/15) and B (kind short-window) below are
  retained as preliminary short-window evidence only and contribute
  NOTHING to the v1 counts.
- client-error live verification: **PASS** (`kind-clienterror/decision2.json`;
  594×400s on the wire excluded, bad=0 both slots). First attempt
  INCONCLUSIVE/insufficient_traffic (980 < 1000) preserved as boundary evidence.
- Grafana screenshots: `results/budgetguard/screenshots/slo-overview.png`
  (healthy traffic) + `slo-burn.png` (error burn rising). Real headless-Chrome
  captures of the provisioned `sre-slo` dashboard.

## Singles (live, native)
- healthy → PASS exit 0 · error → FAIL exit 2 · slow → FAIL exit 2 (contaminated window, debugging only)
- empty Prometheus → INCONCLUSIVE · stale gateway → INCONCLUSIVE · prometheus down → INCONCLUSIVE
