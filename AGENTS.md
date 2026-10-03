# AGENTS.md — sre-portfolio working agreement (stable instructions)

## Scope (v1 release authority: RELEASE_PLAN.md)
- Build three v1.0 products in order: shared lab readiness → BudgetGuard
  v1.0 → FaultLab v1.0 → RecoverOps v1.0 → portfolio v1.0 integration →
  review and v1.1. RecoverOps is AUTHORIZED (R1–R4); it is no longer deferred.
- Preserve baseline commits `0fe8b77`, `cad2f7e` and all existing evidence.
  Never rewrite baselines; new work goes on feature branches.
- FaultLab work lives on `faultlab-dev`; RecoverOps implementation lives on
  a `recoverops-dev` branch (created when R1 starts).
- No publishing, paid infrastructure, messages to others, or deletion of
  unrelated data. No cache deletion or publishing without explicit
  authorization.

## Environment and authorization
- Explicit `kind-sre-lab` context for every cluster mutation (scripts AND
  binaries — see `scripts/require-context.sh`).
- No cache deletion or publishing without explicit authorization.
- Disk floor defaults to 2GiB (`scripts/disk-floor.sh`); the watchdog
  (`scripts/disk-watchdog.sh`) guards long runs. Never lower a threshold
  to bypass a check — fix the cause or record the blocker.

## Evidence rules- Running code, not plans. No TODO stubs, fake benchmark data,
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

## Release process (per RELEASE_PLAN.md)
- Gate statuses: TODO, IN_PROGRESS, BLOCKED, IMPLEMENTED_UNVERIFIED,
  ACCEPTED. Never DONE for code without its required live check.
  Pending external review does not block local delivery.
- After each gate passes, checkpoint and continue automatically; do not
  stop for plan re-approval. One product milestone in progress at a time.
- Fix contract-violating defects inside the active milestone; everything
  optional goes to BACKLOG.md (v1.1). Never invent new release blockers.
- Ledger files: RELEASE_PLAN.md (frozen contract), RELEASE_STATUS.md
  (one current gate table), BACKLOG.md, CHANGELOG.md, docs/decisions.md.
