# HANDOFF — current checkpoint (RecoverOps R2 accepted)

> RECONCILED 2026-10-03 against actual Git state. Prior revision mixed
> `faultlab-dev@679afbd` info with `recoverops-dev` progress. Corrected
> below: branch is `recoverops-dev`, HEAD is `d40825c`. No tests re-run
> in this reconciliation; last recorded greens are cited with their
> commits. Cluster/bench state marked STALE until readiness re-check.

## Storage migration — HALTED, needs owner decision (2026-10-03)
- Intent: move ~/Developer/{google,AnalogDevicesProjects,machineLearning,
  UL_/ta/.venv}, ~/.cache/huggingface, 2× Spider-Man folders to MANU_DISK.
- DONE: `google/` moved whole (295M, verified + git repaired for exFAT).
- PARTIAL: AnalogDevicesProjects via `rsync -a --remove-source-files`
  (resumable after a killed `mv`). Source went 8.1G → ~5.6G, then the
  transfer was SIGTERMed gracefully for investigation (PIDs 62273/62275,
  both exited; no -9, no unmount, no deletion).
- PRESERVED BOTH: `~/Developer/AnalogDevicesProjects` (remainder) and
  `/Volumes/MANU_DISK/AnalogDevicesProjects` (partial). Union is complete:
  rsync unlinks source only after a file lands; the SIGTERM'd partial was
  discarded at dest with its source intact. Already-moved files exist ONLY
  on dest — acknowledged, not independently re-verifiable without re-copy.
- DIAGNOSIS: dest free fell 66G → 13G; only ~3G is my writes. diskutil:
  exFAT, **128 KiB allocation blocks** — small-file waste explains part
  (216k files, many <128K). ~40G+ remains UNATTRIBUTED by bounded checks
  (no hidden writers via lsof; .Trashes uninspectable, SIP-denied; no full
  volume scan — exFAT scans time out). Hypotheses only, no corruption seen,
  no I/O errors seen.
- SAFETY HOLD: dest has ~13G free; remaining moves need ~20-25G with slack.
  DO NOT resume moves until the owner frees dest space or picks a subset.
  SRE benchmarks paused (nothing running; lab idle).

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
- Branch: `recoverops-dev`. HEAD: `d40825c` (2026-10-03 15:22 +0100,
  `HANDOFF: storage migration halted, dest accounting anomaly`).
- Product commits on this branch: `752a02f` R1, `cc4287a` R2
  (both 2026-10-03). Move/handoff commits after R2: `a6cb6a4`,
  `dd48408`, `0837e18`, `d40825c`.
- Prior checkpoint `679afbd` (durable handoff on `faultlab-dev`) is an
  ancestor, not HEAD. Chain since then: `679afbd -> 44e392b ->
  a68b115 -> afaaa95 -> c09f331 -> 87702d4 -> af015ba (BG v1.0) ->
  752a02f (R1) -> cc4287a (R2) -> a6cb6a4 -> dd48408 -> 0837e18 ->
  d40825c`.
- Baselines preserved: `0fe8b77` (master), `cad2f7e` (correctness-fixes).
- NOTE: `RELEASE_STATUS.md:3` still says `Branch: faultlab-dev` — stale,
  needs same fix (not changed in this reconciliation).

## Uncommitted changes and their purpose
- `?? results/faultlab/compare-20260930T001503Z/` (stillborn `set -u`
  crash; `steps.log` + `watchdog.log` only). Nothing else:
  `git status --short --branch` shows `## recoverops-dev` + that path.
- Prior claim “tree clean on faultlab-dev” was STALE (referred to
  `679afbd` era). Do not delete the stillborn dir to make status clean
  per evidence rules; decide explicitly in next session.

## Last completed task
- RecoverOps R2 ACCEPTED on `recoverops-dev` (2026-10-03): policy
  evaluation + observe/enforce proposals, cooldown-restart/budget/mismatch
  tests green, zero cluster writes. R1 also on this branch.

## Current task and next exact action
- Current: R2 is COMMITTED (`cc4287a`), not “committed next” — prior
  wording was contradictory. Latest operational commit is `d40825c`
  (migration HALTED). Lab idle; no benchmarks running.
- Next: RecoverOps R3 (conditional rollback with UID/resourceVersion
  preconditions, restart reconciliation, least-privilege RBAC) per
  contract §8 — first milestone that mutates the cluster (enforce-lab
  only, single target). Prerequisite per `RELEASE_STATUS.md:8`: bounded
  readiness re-check (last green 2026-09-30 8/8 Ready) before any live
  run. Narrow the no-k8s test to the ingest path.

## Commands executed and exit results
- HISTORICAL (pre-move, at `679afbd` post-restart): `go build ./...` OK;
  `go test ./...` 11/11 packages ok (labgateway has no test files;
  faultlab package 40s).
- HISTORICAL (at `af015ba` BG v1.0): `go test ./...` green,
  `./scripts/selftest.sh` ALL PASS, `promtool test rules` SUCCESS 10/10
  (rules unchanged since `cad2f7e`).
- HISTORICAL (FaultLab matrix era): `go test ./...` 11/11 green after
  loadgen retry fix + AttemptID=1; 22 live comparison runs (2 pilot +
  20 matrix, all preserved) — see TASKS.md. Timeout-misclassification
  fix covered by `TestRetryOnceSameKey`.
  Interpretation bound: the 50 `replayed:true` operations are recorded as
  same-key replays only; without per-attempt histories they do not alone
  establish lost-response-after-commit (the deliberate unacked op is the
  controlled ambiguity evidence).
- HISTORICAL: `kubectl apply -k` side effect observed: manifests carry
  `replicas:`, so re-applying scales deployments back up (scale-to-0
  does not survive).
- RECONCILIATION 2026-10-03: no `go test`, `make`, `kubectl`, or
  benchmark re-run. `git status`, `git log`, `df`, `ls results/*`
  read-only. `core.fileMode=false` confirmed set (exFAT).

## Unexecuted tests
- Readiness re-check (TODO per `RELEASE_STATUS.md:8`; last green
  2026-09-30) — required before R3 live work.
- RecoverOps R3/R4 (TODO) + Portfolio integration (TODO).
- External review PENDING throughout (never blocks local delivery).
- Live re-verification of the NEW runner pod/dep paths (unit-tested with
  fakes only; acceptance used standalone CLI connectors on older code).
- BudgetGuard 10-per-class top-ups beyond full-window sets and
  independent external re-review.
- STALE line removed: “FaultLab baseline-versus-resilient comparison;
  RecoverOps (deferred)” — F-04 comparison is ACCEPTED, R1/R2 are
  ACCEPTED, not deferred.

## Running processes, cluster state, disk state
- STALE (2026-09-29 era, superseded by move): post-restart ps clean,
  Docker up, `kind-sre-lab` 8/8 Ready, api-stable 69cwn+wtktz, gateway
  stable-only, 18GiB free. DO NOT rely on this for next run.
- CURRENT (reconciliation, read-only): MANU_DISK 93Gi avail (931G vol,
  839G used); internal data vol 19Gi avail (228G vol, 186G used).
  Prior notes “13G free / 5.4GiB free” referred to migration-time
  pressure, not now — but exFAT 128KiB-block waste + ~40G unattributed
  at the time remain unexplained. Safety HOLD on bulk moves stays until
  owner re-authorizes. Cluster state UNVERIFIED post-move; re-run
  `make doctor` + bounded readiness before benchmarks. No deletions made
  in reconciliation.

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
