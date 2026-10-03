# RELEASE_STATUS.md — one current gate table (v1 authority: RELEASE_PLAN.md)

Branch: `faultlab-dev`. Baselines preserved: `0fe8b77` (master), `cad2f7e` (correctness-fixes).
External review: PENDING throughout; never blocks local delivery.

| Gate | Product | Status | Commit / evidence | Blocker / next action |
|---|---|---|---|---|
| Readiness | Shared lab | TODO | Last green 2026-09-30 (8/8 Ready, PG invariants, routing) | Re-run bounded readiness now |
| B-01 | BudgetGuard | ACCEPTED | `go build`+`go test ./...` green 2026-10-03; `make test-rules` SUCCESS (10/10 fixtures, rules unchanged since `cad2f7e`) | — |
| B-02 | BudgetGuard | ACCEPTED | Full-window 3-class in `kind-fullwindow-20260928T011047Z/` + telemetry-loss INCONCLUSIVE exit 3 with saved query evidence (`results/budgetguard/telemetry-loss-20261003T024957Z/decision.json`) | — |
| B-03 | BudgetGuard | ACCEPTED | 10/10/10 full-window (1001-1003 + 1004-1010), zero INCONCLUSIVE/STOP/failures; `results/budgetguard/kind-fullwindow-20261003T025057Z/` + matrix.md; SUMMARY.md ledger rewritten to full-window-only counts | — |
| B-04 | BudgetGuard | ACCEPTED | Fixed demo ran live 2026-10-03: healthy PASS/exit 0, error/slow FAIL/exit 2, traffic per case (`results/budgetguard/20261003T045748Z/` + exits.log); cleanup-order bug found+fixed+verified; candidate manifest drift reconciled; README/SUMMARY honest | — |
| F-01 | FaultLab | ACCEPTED | `go test ./...` green incl. journal/state/reconcile tests | — |
| F-02 | FaultLab | ACCEPTED | 7 live demos in `results/faultlab/` + `EVIDENCE.md` | — |
| F-03 | FaultLab | ACCEPTED | Journal audit 2026-10-03: intent rows (incl. pod UID target), applied/cleared timestamps, 8 lifecycle events per run, terminals match result.json; oracle CLEAN line on disk | — |
| F-04 | FaultLab | ACCEPTED | 10 pairs / 20 runs, alternating order, command-diff enforced, per-run reseed, 20/20 oracles CLEAN; matches contract counts exactly | — |
| F-05 | FaultLab | ACCEPTED | Generated `report.md` + Methods section (retry sampling, final-only history, recovery/validity definitions) | — |
| R1 | RecoverOps | ACCEPTED | 17 internal + CLI tests green on `recoverops-dev` (reopen, dup/reorder, 503, auth/schema/body rejection, no-k8s-deps); no live gate required (no cluster mutation) | — |
| R2 | RecoverOps | TODO | — | After R1 |
| R3 | RecoverOps | TODO | — | After R2 |
| R4 | RecoverOps | TODO | — | After R3 |
| Portfolio | Integration | TODO | — | After three v1.0s |

Known issue (not a blocker, tracked): runner phase-key reuse — fault phases
re-offer baseline keys when the seed is constant (see TASKS.md). Contract §7
requires phase-disjoint keys with a targeted test; fix inside FaultLab milestone.
