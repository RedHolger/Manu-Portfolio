# HANDOFF — current checkpoint (BudgetGuard fixes + FaultLab acceptance)

## Branch and last commit
- Branch: `faultlab-dev`. HEAD: `f817c53` (runner pod/dep integration,
  ambiguous-response E2E, uniform 2GB floor).
- Baselines preserved: `0fe8b77` (master), `cad2f7e` (correctness-fixes).

## Uncommitted changes and their purpose
- `AGENTS.md` (M): rewritten to stable instructions (this handoff task).
- `TASKS.md` (M): BG-01–BG-10 register added on top of session history.
- `docs/REVIEW.md` (new): full external findings reconstructed from chat
  (the review itself lives nowhere else — this file is now canonical).

## Last completed task
- F3/F4 live acceptance (`d55b52b`): pod deletion, dep-fault TTL cycle,
  oracle CLEAN over 12,139 rows; then runner integration + ambiguity E2E
  + floor hardening (`f817c53`). Full unit suite green at HEAD.

## Current task and next exact action
- Current: durable handoff files (AGENTS/TASKS/HANDOFF/REVIEW) per review.
- Next: commit this checkpoint on `faultlab-dev`; rebuild the inspection
  archive only if disk allows (needs ~16MB free; currently 2.3GiB, fine).
- No live runs головки: next live work (runner pod/dep path re-verification,
  10-per-class top-ups) requires disk ≥2GB at start + watchdog armed.

## Commands executed and exit results
- `go test ./...` at `f817c53`: 11/11 packages ok (last full run).
- `promtool test rules`: SUCCESS, 10/10 (at `cad2f7e`; rules unchanged since).
- `./scripts/selftest.sh`: ALL PASS (at `f817c53`).
- `kubectl apply -k` side effect observed: manifests carry `replicas:`,
  so re-applying scales deployments back up (scale-to-0 does not survive).

## Unexecuted tests
- Live re-verification of the NEW runner pod/dep paths (unit-tested with
  fakes only; acceptance used standalone CLI connectors on older code).
- BudgetGuard 10-per-class top-ups (error/slow kind reps beyond full-window
  3-seed sets) and independent external re-review.
- FaultLab baseline-versus-resilient comparison; RecoverOps (deferred).

## Running processes, cluster state, disk state
- No load/generator/faultlab processes running (verified via ps).
- Docker daemon up. Context `kind-sre-lab`. All 8 lab pods Ready
  (api×4, gateway, postgres, prometheus, grafana) — `apply` re-scaled them;
  idle, no traffic. To pause: scale lab deploys to 0 (see runbook).
- Disk: 2.3GiB free. Floor default 2GB; watchdog available. No deletions made.

## Evidence paths
- `results/SUMMARY.md` (native vs kind, contaminated runs listed).
- `results/budgetguard/kind-fullwindow-*/` (9/9 full-window acceptance).
- `results/faultlab/acceptance-20260929T143244Z/` (F3/F4 live acceptance).
- `results/budgetguard/screenshots/` (2 verified Grafana captures).
- `docs/postmortems/disk-pressure-2026-09-27.md`.

## Unresolved decisions
- None blocking. Open questions for the owner: approve Unity (1.6G) /
  Puppeteer (512M) cache deletion if more headroom is needed; confirm
  whether the next live window should prioritize runner-path re-verification
  or 10-per-class top-ups.
