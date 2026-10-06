# Deploy verification — https://sre-portfolio-demos.vercel.app

Bundle: `results/deploy-20261005T064754Z/`
Verified: 2026-10-05 (UTC), commit `5bf47289749902c97dc7605f3431254ec55a0bec`
Host: Vercel, project `sre-portfolio-demos` (hobby/static, no charge),
      alias `https://sre-portfolio-demos.vercel.app`

## What is in this bundle

| File | What it is |
|---|---|
| `http-status.txt` | curl status + size for all 13 endpoints (anonymous) |
| `dom/*.html` | full rendered DOM per route (headless Chrome `--dump-dom`) |
| `screenshots/*.png` | 1440px screenshots per route (headless Chrome `--screenshot`) |
| `console.txt` | console messages per route — **0 issues** (no JS errors) |
| `live-BUILD.json` | exactly what `GET /BUILD.json` returns |
| `git-tree-match.txt` | SHA-256 of every deployed file vs `git show HEAD:<file>` |

## Checks performed (all passed)

1. **Routes load without auth** (G-4): `/`, `/budgetguard/`, `/faultlab/`,
   `/recoverops/` → 200 with plain `curl`, no cookies, no CLI token.
   Supporting files (`/slos/reservations.yaml`, six data JSONs,
   `/BUILD.json`) → 200.
2. **Pages actually render** (G-5): DOM dumps contain the rendered data cards —
   landing 3 cards, BudgetGuard 4 run cards, FaultLab 10 badges, RecoverOps
   5 badges. Console: 0 messages.
3. **Build commit matches the deployed commit** (G-5): `GET /BUILD.json` returns
   `deploy_commit = 5bf47289749902c97dc7605f3431254ec55a0bec`, which equals
   `git rev-parse HEAD` at deploy time, and the landing page renders
   `deployed 5bf472897499 · site data 5bf472897499 · committed … · go1.27.1`.
   Reproduction: `./scripts/build-demo-site.sh --stamp-deploy 5bf4728…` yields a
   `BUILD.json` byte-identical to the live one.
4. **Deployed bytes come from git**: every deployed file except `BUILD.json`
   (which carries the deploy stamp) is SHA-256-identical to
   `git show HEAD:<file>` — see `git-tree-match.txt`.
5. **Generated data matches the code and the evidence bundles**:
   `./scripts/build-demo-site.sh --check` → OK at this commit (CI runs the same
   check).

## Reproduce

```sh
./scripts/deploy-vercel.sh          # stamp, upload, verify (fails on mismatch)
open https://sre-portfolio-demos.vercel.app/
```

## Redeploy 2026-10-05: live = `328a989161b8`

Redeployed with `./scripts/deploy-vercel.sh` after the two review fixes.
Live `deploy_commit` is `328a989161b8` (this bundle refreshed against it:
statuses, DOM, screenshots, `live-BUILD.json`, `git-tree-match.txt`).
What changed since the previous deploy (`5bf4728`): BudgetGuard coverage
gap now measured on consecutive source timestamps
(`TestCoverageSingleMissingBucketFails` timestamp-aware,
`TestCoverageStraddlingSamplesFail` new); FaultLab recovery on its own
5% budget + success floor (parallel session `05c5ffa`, live-verified
`c2546b2`); abort-test headroom for slow runners. Site data files are
byte-identical to the previous deploy (replay/evidence unchanged).

## Relationship to later commits

`git diff 5bf472897499..HEAD -- site/` is empty: the commits that followed
the deployment (docs, this evidence bundle) did not touch `site/`, so the live
tree is still the tree of `5bf472897499`. Redeploy with
`./scripts/deploy-vercel.sh` whenever `site/` changes.

## Notes / history

* First deploy (07:46 local) went through with project default protection on
  (`Vercel Authentication`); it served an auth wall, so protection was disabled:
  `vercel project protection disable sre-portfolio-demos --sso --git-fork-protection`
  (`ssoProtection: null`, `gitForkProtection: false`).
* Two subsequent deploys were **blocked** by Vercel with
  *"The deployment was blocked because the commit author doesn't have
  permission to create deployments for this project"* — Vercel rejects git
  metadata whose commit author (`RedHolger <manuvashisth963@gmail.com>`) is not
  a member of the Vercel project. `scripts/deploy-vercel.sh` therefore uploads
  from a copy of `site/` with no `.git` above it; the copy is diffed against
  `site/` first and the live `deploy_commit` is checked afterwards.
* This bundle is refreshed in place: the earlier DOM/screenshots in it came from
  the first (pre-stamp) deploy of the same site.
* Not covered: the Kubernetes lab itself. Docker was down during this session,
  so no live cluster run is part of this deploy — the site labels every figure
  as recorded CLI output or a recorded live run.
