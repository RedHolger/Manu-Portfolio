# HANDOFF — current checkpoint (BudgetGuard fixes + FaultLab acceptance)

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
- BudgetGuard v1.0 ALL GATES ACCEPTED (2026-10-03): B-03 top-up 21/21
  (10/10/10 full-window); B-04 demo live with correct exits; manifest
  drift reconciled; readiness green. FaultLab v1.0 already accepted.

## Current task and next exact action
- Current: BudgetGuard v1.0 + FaultLab v1.0 accepted; tree has this
  checkpoint uncommitted. Lab clean (8/8 Ready, stable-only, manifests ==
  live); disk ~9GiB.
- Next: commit, then start RecoverOps R1 on a new `recoverops-dev` branch
  (contract §8: cmd/recoverops + internal/recoverops + migrations +
  policy config; R1 = ingestion + durable state, no cluster mutation).

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
