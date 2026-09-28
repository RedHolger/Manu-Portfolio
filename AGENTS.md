# AGENTS.md — sre-portfolio working agreement

Scope: shared lab + BudgetGuard B1–B4 only (see `docs/decisions.md` D-001).
FaultLab / RecoverOps are deferred; keep metric names, JSONL fields, and
`results/<project>/<run-id>/` bundle shape compatible with them.

## Environment (M0 — partially blocked, see TASKS.md)
- Toolchain target: Go (brew stable 1.27.1, NOT yet installed — disk 547Mi free),
  kind, kubectl 1.32.2 (present), Docker Desktop (daemon DOWN), promtool.
- Do NOT download images or create clusters until disk ≥15–20 GiB free AND
  explicit cleanup approval. No broad `docker system prune` without approval.
- Go install is gated on disk space only, not on Docker. Unit-testable code
  proceeds while container setup is blocked.

## Build order
M0 env-prep → M1 shared lab → B1 parser+math → B2 compiler → B3 evaluator →
B4 live experiment + demo. Complete each milestone's tests before moving on.

## Rules
1. Running code, not plans. No TODO stubs, fake benchmark data, unconditional
   success paths, broad RBAC, or mocks in the live demo path. Test doubles OK
   in unit tests only.
2. Every report bundle: `schema_version, run_id, UTC timestamps, commit SHA,
   versions, policy/config hash, workload seed, environment details`.
3. Preserve failed and inconclusive runs. Never delete a `results/` bundle to
   make a suite green.
4. One real instance of each acceptance scenario before the full seeded suite.
5. Routing cleanup is UNCONDITIONAL: candidate traffic → 0 after every demo
   outcome (PASS/FAIL/INCONCLUSIVE/error/timeout/interrupt), bounded cleanup
   with a fresh context.
6. Before ending a context window: update TASKS.md (files changed, commands
   run, failures, next exact task, decisions). On restart, read TASKS.md +
   `docs/decisions.md` instead of rebuilding.
7. Honest gates: missing prerequisites listed in `make doctor` are diagnostics,
   not a pass. Image digests enter `versions.lock.json` only after resolution.
8. Never claim completion without executing the milestone's live acceptance
   gates; record unexecuted integration checks explicitly.
