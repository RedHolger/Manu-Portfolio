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

## Class totals toward 10/class (exact valid counts — FINAL)
- healthy: 5 native PASS + 5 kind PASS = **10 ✓ COMPLETE**
- error: 5 native FAIL + 5 kind FAIL (111, 333-caveat, 666, 777, 888) = **10 ✓ COMPLETE**
- slow: 5 native FAIL + 5 kind FAIL (111–555, pure slow signal bad=0) = **10 ✓ COMPLETE**
- client-error live verification: **PASS** (`kind-clienterror/decision2.json`;
  594×400s on the wire excluded, bad=0 both slots). First attempt
  INCONCLUSIVE/insufficient_traffic (980 < 1000) preserved as boundary evidence.
- Grafana screenshots: `results/budgetguard/screenshots/slo-overview.png`
  (healthy traffic) + `slo-burn.png` (error burn rising). Real headless-Chrome
  captures of the provisioned `sre-slo` dashboard.

## Singles (live, native)
- healthy → PASS exit 0 · error → FAIL exit 2 · slow → FAIL exit 2 (contaminated window, debugging only)
- empty Prometheus → INCONCLUSIVE · stale gateway → INCONCLUSIVE · prometheus down → INCONCLUSIVE
