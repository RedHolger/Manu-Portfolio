# DEPLOYMENT_PLAN.md — publish source + three public demos

Created 2026-10-05. Live alongside RELEASE_PLAN.md (release contract is
unchanged; this plan covers publication and demos only).

## Baseline

- Branch `recoverops-dev`, HEAD `3f11c8f406a8940f17b57ef63bf64dc0ceae9cc6`.
- Preserved baselines `0fe8b77` (master) and `cad2f7e` (correctness-fixes).
- Working tree at plan time: only untracked `results/**` evidence plus a
  stale empty `results/.recoverops-experiment.lock`. No experiment driver
  or benchmark process was running (verified with `ps`).
- Disk: 92 GiB free on the repo volume. Docker daemon down; kind cluster
  `kind-sre-lab` context exists but is not verified live.

## Repository strategy

- ONE canonical repository: the existing local repository, pushed whole
  (source + docs + a small sanitised evidence set) under the
  authenticated GitHub account `RedHolger`.
- SSH to GitHub already authenticates (`ssh -T git@github.com` →
  `Hi RedHolger!`). `git ls-remote` shows no existing suitable repository
  under that account for the probed names (`sre-portfolio`,
  `SRE-Portfolio`, `portfolio`, `sre-portfolio-public`).
- BLOCKER: no valid GitHub REST credential. The keychain OAuth token
  (`gho_…`, 40 chars, account `141308323`) returns `401 Bad credentials`;
  `gh` CLI is not installed and has no login; no `GITHUB_TOKEN`/`GH_TOKEN`
  in the environment or shell profiles. Creating a repository, enabling
  Pages, and configuring Actions secrets all require API or web auth.
- Publication branch: `publish-demos`, cut from `3f11c8f`. No branch of
  that name exists. Never force-push, never rewrite history, never push
  `results/` bulk evidence or `bin/` build output.
- Excluded from publication: `*.env`, `bin/`, `*-bin`, `*.log`,
  `__pycache__/`, `._*`, `results/**` except a curated
  `docs/evidence/public/` sample set selected later.

## Hosting strategy

- Static demo site, one deployment, three routes:
  `/budgetguard/`, `/faultlab/`, `/recoverops/`.
- Hosting: **Vercel** — CLI `v47.0.1` is already authenticated as
  `manuvashisth` in team `manus-projects-ffd06e65` (existing projects
  `notes`, `manu-masters-portfolio`; static hobby-tier deployments, no
  charges, no payment entry). GitHub Pages is the fallback once a GitHub
  credential exists (Pages must be enabled via API/web otherwise).
- Deployment runs from repository CI (`.github/workflows/`) once a GitHub
  credential exists; until then a local `vercel deploy --prod` from the
  `publish-demos` branch is the bounded path and is recorded as
  `deployed locally, CI pending`.

## Public-demo safety boundaries

- No public Kubernetes control plane, no fault-injection admin endpoint,
  no execute/patch/rollback/webhook action reachable by anonymous users.
- BudgetGuard demo: precomputed decision documents produced by the
  canonical Go CLI (`budgetguard replay`/`evaluate`), served as static
  JSON; any browser-side re-evaluation is contract-tested against the Go
  output and labelled.
- FaultLab demo: sanitised journal events / explicitly labelled
  simulation, read-only.
- RecoverOps demo: read-only state machine over sanitised ingestion
  evidence; fixture-driven rows are labelled separately from measured
  runtime evidence.
- Public assets must contain no tokens, kubeconfigs, DSNs, internal
  cluster addresses or privileged endpoint URLs.

## Acceptance gates

| Gate | Requirement | Status (2026-10-05) |
|---|---|---|
| G-0 findings | Findings A–E either fixed with regression tests that run green, or documented as unresolved with reasons (no false completion) | ACCEPTED at `cb4da08` |
| G-1 hygiene | Secret scan of tracked files + history before first push; no credentials in tree or history | ACCEPTED at `9bddb19`; re-scanned at `865b031` (site + CI clean) |
| G-2 pages | `docs/projects/{budgetguard,faultlab,recoverops}.md` + rewritten root README exist and match reality | ACCEPTED at `ba51daa` |
| G-3 build | `go build ./...`, `go test ./...`, `go vet ./...`, `gofmt -l`, `make test-rules`, `make test-scripts` green locally | ACCEPTED at `cb4da08`; re-verified in CI (run 4, `89d0c72`) |
| G-4 deploy | Site deployed to a real, no-charge URL; three routes load without auth | ACCEPTED — `https://sre-portfolio-demos.vercel.app/`, 13 endpoints 200 without auth |
| G-5 verify | Each URL opened, refreshed, exercised, console/network checked, screenshots captured, build commit displayed matches deployed commit | ACCEPTED — `results/deploy-20261005T064754Z/` (DOM, screenshots, 0 console issues, `deploy_commit` = `5bf4728`) |
| G-6 outputs | `PUBLIC_LINKS.md` + three LaTeX CV snippets using only verified URLs | ACCEPTED — `PUBLIC_LINKS.md`, `CV_SNIPPETS.tex` |

## Current blockers

1. GitHub repository / Actions — RESOLVED 2026-10-05: created as
   `RedHolger/Manu-Portfolio` (public, empty), `origin` re-pointed, all five
   branches pushed (baselines `0fe8b77`/`cad2f7e` intact), CI green on run 4
   (`89d0c72`, all four jobs). Cause of the first two failures: six shebang
   scripts were tracked 100644 because this checkout has
   `core.fileMode=false`; fixed with `update-index --chmod=+x` and guarded by
   a CI step. Run 3 failed a unit test on the runner (log not readable
   without admin rights); `go test` output is now annotated so a recurrence
   names the test.
2. GitHub Pages — optional and not chosen: the demo site is hosted on Vercel
   (`PUBLIC_LINKS.md`); enabling Pages later must not double-publish the same
   site at an unverified URL.
3. Live lab runs (Docker/kind down) — FaultLab recovery-health and phase
   behaviour beyond the unit/regression tests stay IMPLEMENTED_UNVERIFIED.

## Exact next command

```sh
GIT_SSH_COMMAND="ssh -o BatchMode=yes -o ConnectTimeout=8" git ls-remote --heads origin
# then: open https://github.com/RedHolger/Manu-Portfolio/actions and read the ci run
```
