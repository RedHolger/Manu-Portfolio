# HANDOFF — current checkpoint (RecoverOps R4 implemented-unverified)

> RECONCILED 2026-10-04 against actual Git state. Branch is
> `recoverops-dev`, HEAD is `45066b3`; corrected source fixes are committed.
> `go test ./...`, vet, targeted race tests, Python compilation, shell syntax
> checks, and `git diff --check` pass. No live redeploy or benchmark was run.
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
- Branch: `recoverops-dev`. HEAD: `af24782` (2026-10-03 18:26 +0100,
  `RESOLVED requires persisted verification (verify endpoint + gating)`).
- Product commits on this branch: `752a02f` R1, `cc4287a` R2
  (both 2026-10-03). Move/handoff commits after R2: `a6cb6a4`,
  `dd48408`, `0837e18`, `d40825c`.
- Prior checkpoint `679afbd` (durable handoff on `faultlab-dev`) is an
  ancestor, not HEAD. Chain since then: `679afbd -> 44e392b ->
  a68b115 -> afaaa95 -> c09f331 -> 87702d4 -> af015ba (BG v1.0) ->
  752a02f (R1) -> cc4287a (R2) -> a6cb6a4 -> dd48408 -> 0837e18 ->
  d40825c`.
- Follow-up commits after that checkpoint: `dea3eb7`, `3c3e8a0`,
  `5e563aa`, `667ddde`, `129887d`, `648ad94`, `af24782`.
- Baselines preserved: `0fe8b77` (master), `cad2f7e` (correctness-fixes).
- `45066b3` is the current committed corrected-source checkpoint; preserved
  result directories remain untracked and unstaged.

## Uncommitted changes and their purpose
- RecoverOps code/tests add server-measured Prometheus windows,
  persisted verification gating, resource-version/replica fields, and
  CLI/config wiring. The working tree also fixes `sumVector` to read the
  Prometheus sample value rather than the complete `[timestamp,value]`
  tuple.
- `results/recoverops/pairs/pair-02/` is a completed valid RV2 pair:
  baseline and controller both resolved with passing 3x150 windows.
- `results/recoverops/pairs/pair-03/` contains only `floor.txt`; no arm
  ran and it is not a valid pair.
- `results/faultlab/compare-20260930T001503Z/` remains preserved
  stillborn evidence; do not delete it to make status clean.

## Last completed task
- R4 persisted verification is implemented in the working tree and
  covered by the full Go suite. The endpoint measures three consecutive
  10s Prometheus windows itself; callers cannot supply passing counts.
- Valid RV2 evidence currently includes pair-02; pair-01 remains
  grandfathered RV1 evidence. Pair-03 stopped before execution.
- R3 remains ACCEPTED live via
  `results/recoverops/r3-live-20261003T151252Z/`; R1/R2 are ACCEPTED.

## Current task and next exact action
- Current: R3 ACCEPTED live; R4 IMPLEMENTED_UNVERIFIED. Genuine AM path
  proven (`0bc3b543`); persisted verification now requires live-template
  equality plus server-measured passing windows. Matrix pair-02 is valid,
  pair-03 is preflight-only, and pairs 04-11 are pending.
- Matrix CHECKPOINTED after pair-02. Resume only after committing and
  redeploying the fix, then rechecking readiness:
  `nohup bash -c 'i=3; seed=340; for order in controller-first baseline-first controller-first baseline-first controller-first baseline-first controller-first baseline-first controller-first; do dir=results/recoverops/pairs/pair-$(printf %02d $i); mkdir -p "$dir"; ./scripts/r4-pair.sh "$dir" $seed $order >> results/recoverops/pairs/matrix.log 2>&1; i=$((i+1)); seed=$((seed+20)); done' > /tmp/matrix3.log 2>&1 &`
  exec.log paces budget (do not delete). Pair ledger:
  `results/recoverops/pairs/LEDGER.md`; count valid pairs and preserve
  failures.
- Storage: HOLD continues (migrations untouched; untracked
  `compare-20260930T001503Z/` preserved, not evidence).
- Next: commit and redeploy the verified fix, complete the remaining
  matrix, then run the full R4 demo timeline and portfolio integration
  demo. Do not repeat accepted B/F benchmarks without concrete reason.

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
- RECONCILIATION 2026-10-04: `go test ./...` green and
  `git diff --check` clean. No live redeploy or benchmark was run.

## Unexecuted tests
- Live redeployment/re-verification of the server-measured endpoint after
  the current fix.
- Remaining R4 matrix pairs and Portfolio integration.
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
- `results/recoverops/pairs/pair-02/` (valid RV2 pair).
- `results/recoverops/pairs/pair-03/` (preflight-only disk-floor record).

## Unresolved decisions
- None blocking. Open questions for the owner: approve Unity (1.6G) /
  Puppeteer (512M) cache deletion if more headroom is needed; confirm
  whether the next live window should prioritize runner-path re-verification
  or 10-per-class top-ups.

## Corrected source-fix application — 2026-10-04
- Checkpointed the pre-existing validated RecoverOps work as commit
  `3a1a158` before applying the corrected source bundle.
- Applied the non-overlapping portions of
  `/Volumes/MANU_DISK/google/sre-portfolio-corrected/corrections.patch`.
  The existing server-measured verification implementation was replaced with
  the bundle's autonomous `VerifyIncident` implementation; historical evidence
  directories were preserved.
- Added durable execution intents and UID-bound registrations, conditional
  Kubernetes template patches, controller locking/reconciliation, corrected
  experiment/demo tooling, loadgen write-error propagation and isolated keys,
  stricter context/disk guards, and the related deployment/migration/docs.
- Offline validation after application: `go test ./...`, Python compilation,
  shell syntax checks, and `git diff --check` passed. No live cluster,
  Prometheus, Alertmanager, PostgreSQL, matrix, or portfolio acceptance was
  run; R4 and Portfolio remain `IMPLEMENTED_UNVERIFIED`.
