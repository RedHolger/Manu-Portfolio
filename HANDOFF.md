# HANDOFF — current checkpoint (RecoverOps R2 accepted)

## Project location (moved 2026-10-03 for disk space)
- New: `/Volumes/MANU_DISK/google/sre-portfolio` (exFAT external volume).
- Old `~/Developer/google` (295M: repo + docs + tex) moved wholesale; source
  path no longer exists. Local git `core.fileMode=false` set (exFAT
  synthesizes +x on everything); AppleDouble `._*` sidecars from the move
  were deleted (broke git pack enumeration; content verified intact).
- Verified post-move: `git status` clean, `go build ./...` OK, recoverops +
  faultlab suites green, SQLite WAL probe on exFAT OK.
- NOTE: the three previously reported ZIP archives were already absent from
  `~/Developer/google` before the move (not moved, not deleted by agent).

## Branch and last commit
- Branch: `faultlab-dev`. HEAD: `679afbd` (durable handoff: stable AGENTS,
  BG register, checkpoint HANDOFF, full REVIEW).
- Baselines preserved: `0fe8b77` (master), `cad2f7e` (correctness-fixes).
- Full chain verified post-restart: `0fe8b77 -> cad2f7e -> df08ed9 ->
  26e2fe3 -> 4cd142b -> d124ce6 -> d55b52b -> f817c53 -> 679afbd`.

## Uncommitted changes and their purpose
- None. Tree clean (`git status --short --branch` shows only `## faultlab-dev`).
- The prior handoff's uncommitted AGENTS/TASKS/REVIEW docs are committed
  in `679afbd`; its "commit this checkpoint" next action is DONE.

## Last completed task
- RecoverOps R2 ACCEPTED on `recoverops-dev` (2026-10-03): policy
  evaluation + observe/enforce proposals, cooldown-restart/budget/mismatch
  tests green, zero cluster writes. R1 also on this branch.

## Current task and next exact action
- Current: R2 committed next (this checkpoint) on `recoverops-dev`.
  Lab idle and clean; disk 5.4GiB free (down from 9 — Docker VM growth
  over the live day; floor still held, watch before further long runs).
- Next: RecoverOps R3 (conditional rollback with UID/resourceVersion
  preconditions, restart reconciliation, least-privilege RBAC) per
  contract §8 — first milestone that mutates the cluster (enforce-lab
  only, single target). Narrow the no-k8s test to the ingest path.

## Commands executed and exit results
- `go build ./...` at `679afbd` post-restart: OK (exit 0).
- `go test ./...` at `679afbd` post-restart: 11/11 packages ok
  (labgateway has no test files; faultlab package 40s).
- Prior at HEAD: `go test ./...` 11/11 green, `./scripts/selftest.sh` ALL
  PASS, `promtool test rules` SUCCESS 10/10 (rules unchanged since `cad2f7e`).
- This session: `go test ./...` 11/11 green after loadgen retry fix +
  AttemptID=1; 22 live comparison runs (2 pilot + 20 matrix, all
  preserved) — see TASKS.md for counts. Timeout-misclassification fix
  covered by new `TestRetryOnceSameKey`.
  Interpretation bound: the 50 `replayed:true` operations are recorded as
  same-key replays only; without per-attempt histories they do not alone
  establish lost-response-after-commit (the deliberate unacked op is the
  controlled ambiguity evidence).
- `kubectl apply -k` side effect observed: manifests carry `replicas:`,
  so re-applying scales deployments back up (scale-to-0 does not survive).

## Unexecuted tests
- Live re-verification of the NEW runner pod/dep paths (unit-tested with
  fakes only; acceptance used standalone CLI connectors on older code).
- BudgetGuard 10-per-class top-ups (error/slow kind reps beyond full-window
  3-seed sets) and independent external re-review.
- FaultLab baseline-versus-resilient comparison; RecoverOps (deferred).

## Running processes, cluster state, disk state
- Post-restart (2026-09-29): no load/generator/faultlab processes (ps clean).
- Docker up (started this session). Context `kind-sre-lab`, node Ready.
  All 8 lab pods Ready; api-stable now 69cwn (pod-run replacement) + wtktz.
  Gateway faults empty, dep-fault disarmed, routing stable-only. Idle.
- Disk: 18GiB free (19.5 at Docker start). Floor 2GB held at every step;
  watchdog armed for the run, never tripped (no STOP in the new run dir).
  The old acceptance STOP stays preserved as historical evidence.
  No deletions made.

## Evidence paths
- `results/SUMMARY.md` (native vs kind, contaminated runs listed).
- `results/budgetguard/kind-fullwindow-*/` (9/9 full-window acceptance).
- `results/faultlab/acceptance-20260929T143244Z/` (F3/F4 live acceptance).
- `results/faultlab/runner-acceptance-20260929T221906Z/` (NEW: dep-abort +
  dep-full + pod runner runs with journals, scenarios, ambiguous oracle).
- `results/faultlab/compare-20260930T001528Z/` (pilot pair: before-fix
  record, resilient retried nothing) and `compare-20260930T002156Z/`
  (20-run matrix + generated `report.md`).
- `results/budgetguard/screenshots/` (2 verified Grafana captures).
- `docs/postmortems/disk-pressure-2026-09-27.md`.

## Unresolved decisions
- None blocking. Open questions for the owner: approve Unity (1.6G) /
  Puppeteer (512M) cache deletion if more headroom is needed; confirm
  whether the next live window should prioritize runner-path re-verification
  or 10-per-class top-ups.
