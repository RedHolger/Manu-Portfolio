# PUBLIC_LINKS.md — verified public URLs for the demo site

Last verified: 2026-10-05 (UTC), site deploy `5bf472897499`, repository
push at `b4933ef…` — see
`results/deploy-20261005T064754Z/` for the raw evidence (HTTP statuses, DOM
dumps, screenshots, console output, git-tree comparison).

Every URL below was opened anonymously (no login, no Vercel CLI, no cookie) and
returned 200; the pages were rendered headlessly and checked for console
errors. Nothing here is a URL I have not fetched.

## Demo site (static, no charge — Vercel hobby)

| URL | What it shows | Provenance of what you see |
|---|---|---|
| <https://sre-portfolio-demos.vercel.app/> | Landing page, links to the three routes, build stamp | static |
| <https://sre-portfolio-demos.vercel.app/budgetguard/> | BudgetGuard SLO compiler replay: 4 recorded runs (PASS / FAIL / FAIL / INCONCLUSIVE) | recorded CLI output from `bin/budgetguard` over `tests/fixtures/releases/*.json` |
| <https://sre-portfolio-demos.vercel.app/faultlab/> | FaultLab journaled experiment: passed run + aborted run, event stream, recorded faults | read-only dumps of committed `results/faultlab/demo2-expire/j.db` and `demo6-abort/j.db` |
| <https://sre-portfolio-demos.vercel.app/recoverops/> | RecoverOps R3 recovery incident: detection → proposal → execute → verify, phases and events | read-only dump of committed `results/recoverops/r3-live-20261003T151252Z/r3.db` |

Supporting files (also verified 200):

- <https://sre-portfolio-demos.vercel.app/BUILD.json> — `deploy_commit` must equal
  the commit you are looking at in git; `scripts/deploy-vercel.sh` fails if it does not.
- <https://sre-portfolio-demos.vercel.app/slos/reservations.yaml> — the SLO input.
- `<route>data/*.json` — the generated data files the pages read.

## What is *not* claimed

- The live lab (kind cluster, PostgreSQL, Prometheus, load generator) is **not**
  exposed to the internet. Nothing on the site can inject faults, roll back, or
  patch a cluster.
- FaultLab and RecoverOps pages show **recorded evidence** from committed
  `results/` bundles; only BudgetGuard's four runs are re-executed by the
  generator script, and the live lab itself is not running.
- RecoverOps **R4 (fault-injection plane) is unverified**; the site says so.
- These pages are static. There is no backend, no account, no data collection.

## Source repository

| URL | Status |
|---|---|
| <https://github.com/RedHolger/Manu-Portfolio> | **VERIFIED** 2026-10-05: `git ls-remote origin HEAD` → `b4933ef…` (all five branches present: `publish-demos` (default), `master` @ `0fe8b77`, `recoverops-dev`, `faultlab-dev`, `correctness-fixes`). |
| <https://github.com/RedHolger/Manu-Portfolio/actions> | CI workflow `ci.yml` (jobs go/rules/scripts/site) — see run status before quoting it as passing. |

Baselines `0fe8b77` (master) and `cad2f7e` (correctness-fixes) were pushed as-is;
no history was rewritten and no `results/` bundle was deleted.
