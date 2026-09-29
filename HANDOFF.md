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
- Runner pod/dep live acceptance (2026-09-29): dep FAILED(abort) correct +
  dep PASSED + pod PASSED via `faultlab run` with journaled intent;
  deliberate ambiguous oracle case CLEAN (1 reconciled, 0 violations).
  `pod_delete` parsing enabled (was F3-gated); full suite 11/11 green.
  Evidence: `results/faultlab/runner-acceptance-20260929T221906Z/`.

## Current task and next exact action
- Current: all authorized runner-path live acceptance complete. Lab idle:
  no port-forwards, no watchdog, no lab processes; api-stable 2/2 Ready
  (69cwn replacement + wtktz); disk 18GiB; tree has code + TASKS/HANDOFF
  updates uncommitted.
- Next: commit this checkpoint locally. Then STOP live work: remaining
  FaultLab comparison + BudgetGuard top-ups need a fresh authorized
  window (disk ≥2GB + watchdog). Independent source re-review is a
  separate non-runtime track (per owner correction) — not blocked on Docker.
- Live work still queued (unchanged): baseline-versus-resilient comparison,
  10-per-class top-ups.

## Commands executed and exit results
- `go build ./...` at `679afbd` post-restart: OK (exit 0).
- `go test ./...` at `679afbd` post-restart: 11/11 packages ok
  (labgateway has no test files; faultlab package 40s).
- Prior at HEAD: `go test ./...` 11/11 green, `./scripts/selftest.sh` ALL
  PASS, `promtool test rules` SUCCESS 10/10 (rules unchanged since `cad2f7e`).
- This session: `go test ./...` 11/11 green after the `pod_delete` parser
  change; 3 live runner runs (dep-abort FAILED-correct, dep-full PASSED,
  pod PASSED) + ambiguous oracle CLEAN — see TASKS.md for counts.
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
- `results/budgetguard/screenshots/` (2 verified Grafana captures).
- `docs/postmortems/disk-pressure-2026-09-27.md`.

## Unresolved decisions
- None blocking. Open questions for the owner: approve Unity (1.6G) /
  Puppeteer (512M) cache deletion if more headroom is needed; confirm
  whether the next live window should prioritize runner-path re-verification
  or 10-per-class top-ups.
