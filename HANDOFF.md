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
- F3/F4 live acceptance (`d55b52b`): pod deletion, dep-fault TTL cycle,
  oracle CLEAN over 12,139 rows; then runner integration + ambiguity E2E
  + floor hardening (`f817c53`). Full unit suite green at HEAD.

## Current task and next exact action
- Current: post-restart recovery (2026-09-29). Verified: tree clean at
  `679afbd`, `go build ./...` OK, `go test ./...` 11/11 packages green.
- Next: start Docker Desktop (`open -a Docker`), wait for daemon, then
  `kubectl get pods -n sre-lab` on context `kind-sre-lab` to see whether
  the 8 lab pods survived the reboot or the kind node needs recreation.
  No live runs until cluster state is inspected; disk ≥2GB + watchdog
  still required before any live work.
- Live work still queued (unchanged): runner pod/dep path re-verification,
  10-per-class top-ups, baseline-versus-resilient comparison.

## Commands executed and exit results
- `go build ./...` at `679afbd` post-restart: OK (exit 0).
- `go test ./...` at `679afbd` post-restart: 11/11 packages ok
  (labgateway has no test files; faultlab package 40s).
- Prior at HEAD: `go test ./...` 11/11 green, `./scripts/selftest.sh` ALL
  PASS, `promtool test rules` SUCCESS 10/10 (rules unchanged since `cad2f7e`).
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
- Docker daemon DOWN (Docker.app present, `docker info` fails; kind cannot
  list clusters). kubectl context reads `kind-sre-lab` but the API server
  at 127.0.0.1:52831 refuses connection — cluster unreachable until Docker
  Desktop starts. Prior "8/8 pods Ready" state is NOT assumed to survive.
- Disk: 17GiB free (Data vol 92%) — well above the 2GB floor, up from the
  1.3–2.3GiB reported pre-restart (reboot + stopped Docker VM freed space).
  Cause of the jump not forensically established; re-check before live runs.
  No deletions made.

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
