# AGENTS.md — sre-portfolio working agreement (stable instructions)

## Scope
- Shared lab + BudgetGuard (B1–B4); FaultLab F1–F4 in progress on
  `faultlab-dev`; RecoverOps deferred.
- Preserve baseline commit `0fe8b77` and all existing evidence. Never
  rewrite baselines; new work goes on feature branches.
- Correctness fixes live on `correctness-fixes` (BudgetGuard review);
  FaultLab work lives on `faultlab-dev`.

## Environment and authorization
- Explicit `kind-sre-lab` context for every cluster mutation (scripts AND
  binaries — see `scripts/require-context.sh`).
- No cache deletion or publishing without explicit authorization.
- Disk floor defaults to 2GiB (`scripts/disk-floor.sh`); the watchdog
  (`scripts/disk-watchdog.sh`) guards long runs. Never lower a threshold
  to bypass a check — fix the cause or record the blocker.

## Evidence rules
- Running code, not plans. No TODO stubs, fake benchmark data,
  unconditional success paths, broad RBAC, or mocks in live paths.
  Test doubles are allowed in unit tests only.
- Every report bundle keeps: schema_version, run_id, UTC timestamps,
  commit SHA, versions, policy/config hash, workload seed, environment.
- Preserve failed, inconclusive, and contaminated runs with reasons.
  Never delete a `results/` bundle to make a suite green.
- Never mark a task complete merely because code was written: only
  executed tests and measured outcomes count. Unverified stays unverified.

## Session protocol
- Before ending a context window: update TASKS.md and HANDOFF.md.
  Save long-running command output to a task-specific log.
- Commit coherent, verified changes locally; do not push.
- On restart, read AGENTS.md, HANDOFF.md, TASKS.md, docs/REVIEW.md;
  inspect `git status` and recent commits; reconcile handoff against
  actual files and running processes before continuing.
