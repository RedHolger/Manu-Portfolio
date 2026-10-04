# Corrected source handoff — 2026-10-03

This is an implementation update to the uploaded source, not a claim that the
new version passed the user's live lab. The supplied ZIP SHA-256 is
`624dddb4035408e705519057902002ebe4f560467191d016cfc2ab9a731bd8e3`.
The original archive and all extracted results were preserved. The downloadable
source package excludes original Git history, credentials, generated binaries,
logs, and runtime databases/results. Apply its patch to your existing repository
to retain those local artifacts. Historical handoff/status files are under
`docs/history/`; they are not current runtime observations.

## What was fixed

- RecoverOps now executes only a persisted enforce-lab proposal for the same
  policy, target and explicitly registered **Deployment UID**. Template hashes
  use the same canonicalization at registration and comparison.
- An immutable SQLite intent precedes the Kubernetes call. It records the
  original UID/template, desired bytes, attempts and timestamps. Restarts use
  that intent; they cannot adopt an operator's changed template as a new baseline.
- Each action reserves cooldown/hourly budget atomically at execution, including
  ambiguous attempts. Old executed rows still count after upgrade. A queued
  proposal cannot bypass a newer reservation. One logical action permits at most
  two conditional patch attempts, with a 180-second execution deadline.
- JSON Patch tests UID and resourceVersion before replacing only `spec.template`.
  Other Deployment fields are not sent back in an Update. Lost responses are
  reconciled with a fresh read; third-party edits/replacements escalate.
- The controller resumes pending work and measures recovery autonomously. A
  resolved webhook or client-supplied counts cannot authorize RESOLVED. The
  verified event and terminal state commit in one transaction; cancellation wins.
- Verification fixes the Prometheus sample-value indexing defect, retains
  fractional estimates, requires three timestamped post-action 10-second
  windows, checks sample freshness/coverage and matching success/bucket series,
  observed generation, UID/hash, and full updated/available replicas. Each window
  needs >=100 eligible requests, >=99% success and >=99% joint fast success.
  The 180-second verification deadline never authorizes another rollback.
- A process lock and Recreate deployment enforce a single controller per SQLite
  database. RBAC is restricted to get/patch of api-stable. Kubeconfig selection
  and shell context guards cannot redirect mutations through an environment
  override. No HA/distributed-lock claim is made.
- Loadgen can isolate keys per arm while retaining identical seeded fault draws;
  output write failures propagate and truncated workloads are invalid. FaultLab
  result-write errors also propagate. Existing phase-disjoint seeds are retained.
- The experiment driver configures a real 120-second baseline delay **before**
  injecting the fault, uses real alert receipt/action/verification timestamps,
  respects durable budgets, alternates arm order, and requires valid measured
  pairs. Historical RV1/grandfathered rows never enter the new schema-3 matrix.
- Drivers emit progress every 30 seconds, poll at five-second intervals with
  bounded deadlines, own their subprocesses, preserve interrupted outcomes,
  stop below 2GiB on either filesystem, and conditionally restore only templates
  and controller arguments that they still own. STOP is never removed on resume.

## Apply without replacing your repository

First allow any old active arm to finish or halt it with its existing cleanup
procedure and preserve its evidence. Do not run old and corrected drivers together.
From your existing repository, inspect `git status`. Commit/checkpoint your own
tracked edits if needed; retain the untracked comparison evidence.

```bash
git switch -c recoverops-source-fixes
# PATCH is the absolute path to corrections.patch from the downloaded package.
git apply --check "$PATCH"
git apply "$PATCH"
go test ./...
go vet ./...
# Review and stage only source/docs changes, not results or credentials.
git add -u
git add cmd internal scripts deploy migrations docs HANDOFF.md RELEASE_STATUS.md
# Inspect the staged paths before committing.
git diff --cached --stat
git commit -m "Harden remediation execution, verification and experiment runners"
```

If `git apply --check` fails because local files advanced, stop and reconcile the
reported files; do not force the patch or overwrite the repository with the ZIP.
The package's source directory is an alternative fresh checkout reference, not a
replacement for local history/evidence. The package's synthetic import/fix commits
are not descendants of the user's `recoverops-dev` history.

## Upgrade and run the existing lab

All following live steps are **unexecuted here**. Docker/kind, PostgreSQL,
Prometheus/promtool and the user's cluster were unavailable. Keep unrelated
storage migrations on HOLD. Nothing here recreates the cluster or resets a DB.
Use the existing `kind-sre-lab` context; no context switching is implicit.

1. Start the existing Docker installation if necessary. Run `make doctor`,
   `./scripts/require-context.sh`, `./scripts/disk-floor.sh`, and
   `./scripts/wait-ready.sh`. Investigate a failed check before continuing.
2. Export `LAB_ADMIN_TOKEN` and `RECOVEROPS_TOKEN` privately in the shell. Use
   tokens without surrounding whitespace. Do not paste token values into logs.
   Stop/checkpoint the old controller before its first upgraded rollout. Make a
   consistent SQLite backup using SQLite's backup mechanism or the stopped
   database files, including WAL/SHM if present. Preserve existing journals.
3. Run `make setup-recoverops`. This builds for the actual kind-node architecture,
   loads images, applies the named lab manifests and secrets, then waits on
   rollouts. Existing Postgres secrets/PVC/schema must already exist. This is an
   existing-lab upgrade, not a fresh-machine bootstrap. Image digests in
   `versions.lock.json` describe previous builds: capture the new runtime imageIDs
   (the drivers do so) before making a new release lock/tag.
4. Migration 002 creates intent/binding tables idempotently on database open.
   Unbound old known-good rows cannot authorize execution. Legacy in-flight
   execution/verification without an immutable intent escalates for operator
   reconciliation; historical terminal rows are unchanged. Cancel/reconcile any
   stale nonterminal incident before new experiments. Do not edit history into a
   passing state.
5. With the API healthy and no load/fault in flight, run
   `./scripts/prepare-stock.sh 250000`. This additive operation raises available
   **and** initial stock by the same amount under a transaction, after checking
   conservation. It deletes no reservations. Save its output with the run. It is
   needed because a full 22-arm experiment can consume more than 100,000 units.
6. Establish fresh healthy traffic and register the current UID/template:

```bash
# Terminal A — keep this forward alive for registration only.
kubectl --context kind-sre-lab -n sre-lab port-forward svc/labgateway 18080:8080
# Terminal B — use a new prefix for each registration attempt.
bin/labload run --gateway http://127.0.0.1:18080 --rate 15 --duration 120s \
  --seed 499 --key-prefix "registration-$(date -u +%Y%m%dT%H%M%SZ)" \
  --output /tmp/registration-traffic.jsonl
# Terminal C — after at least 40 seconds of healthy traffic, while B still runs.
make register-recoverops
```

   Registration stops the controller, runs a unique retained Job against its
   SQLite PVC, verifies real healthy windows/rollout, binds the actual UID, then
   restores the previous replica count. A failed check remains a failed check.
   Stop Terminal A's forward after Terminal B finishes; the experiment owns its
   own forwards and needs those ports free. Keep registration logs/artifacts.

7. Run `make test-rules` and `make test-scripts` on the local host. Then start one
   corrected matrix with a **new output directory**, not the prior RV1/RV2 root:

```bash
python3 scripts/recoverops-experiment.py matrix \
  --out results/recoverops/corrected-matrix-NEW-ID --seed 500
python3 scripts/recoverops-report.py results/recoverops/corrected-matrix-NEW-ID \
  --require-complete
```

   This is one pilot plus ten measured pairs (22 arms), not ten individual runs.
   The 3/hour policy makes this a multi-hour run. Heartbeats distinguish policy
   waiting from a stuck command. Baseline measures a configured delay; it does
   not claim to measure human operator response. Resume the same command only
   with the same committed source/config and valid prior arms. An incomplete or
   invalid arm is retained; investigate and choose a new attempt directory.
   Do not silently delete STOP or select only passing seeds.
8. Run `make demo-portfolio`. It runs three full-window BudgetGuard cases,
   a journaled TTL FaultLab scenario, then a genuine RecoverOps alert-to-verified
   recovery. It pauses remediation during the first two products and restores it.
   Results are under a new `results/portfolio/` directory. `make demo-budgetguard`
   uses the same safe BudgetGuard implementation without requiring RecoverOps
   when its Deployment is absent. Build the binaries first in either case.
9. Only after those measured gates pass: update RELEASE_STATUS and the image lock,
   reconcile README claims with artifacts, and create local release tags. No
   release tag, push, accuracy claim or improved recovery-time claim was created
   in this source-fix session.

## Acceptance still required

- Corrected RecoverOps migration/registration, restart and lost-response paths in
  kind; real Prometheus source-series coverage; real Alertmanager delivery.
- One corrected pilot + ten valid paired runs and the combined demo.
- Local process-matching watchdog selftest (not supported by this execution
  environment's virtual PID table), promtool validation, PostgreSQL top-up
  transaction and setup/registration jobs.
- Independent external review if desired. Do not treat this implementation pass
  as independent third-party review.

BudgetGuard/FaultLab prior evidence remains available in the original repository.
No prior successful or failed experiment was deleted or relabelled as a new-code
run. Resume instructions and test results are authoritative for this delivery;
older docs/evidence describe their own versions.
