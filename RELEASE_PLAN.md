# SRE portfolio: versioned implementation and agile delivery guide

Version 1 · 3 October 2026 · For Manu and the local OpenCode agent

## 1. Delivery agreement

Finish three usable v1.0 products in this order:

**Shared lab readiness → BudgetGuard v1.0 → FaultLab v1.0 → RecoverOps v1.0 → portfolio v1.0 integration → review and v1.1 improvements.**

This guide freezes the initial release scope. Do not stop after every milestone to ask ChatGPT for more suggestions. Continue automatically after the listed acceptance gates pass. Put optional ideas into the versioned backlog. A correctness defect that makes a gate unsafe or its evidence false must be fixed within that milestone; cosmetic changes and broader features wait.

This is a delivery guide and implementation contract, not an independent certification that the current source is correct. The earlier Google_SRE_OpenCode_Implementation.md remains useful detailed background. Where this guide explicitly narrows or reschedules its work, this guide controls the release scope. Preserve historical decisions and append the superseding decision rather than erasing history.

The user has now authorized work through all three local implementations, including RecoverOps. Update repository instructions that still say RecoverOps is deferred to reflect this new scope. This does not authorize publishing, paid infrastructure, messages to others, or deletion of unrelated data.

## 2. Starting point inspected

Input: `sre-portfolio-complete-20261003T022709Z.zip`.

| Area | What is present in the archive | Next action |
|---|---|---|
| Shared lab | Reservation API, gateway, load generator, SQL migrations, kind manifests | Reuse; perform a bounded readiness check |
| BudgetGuard | CLI, compiler, coverage/evaluation code, tests and results | Reconcile acceptance status; finish only missing v1 gates |
| FaultLab | Journal, runner with pod/dependency backends, oracle, CLI and comparison harness | Reconcile evidence; close only missing v1 gates |
| Comparisons | Baseline/resilient configs and saved September 30 results | Reuse valid measurements; do not repeat merely to generate newer files |
| RecoverOps | No `cmd/recoverops` or `internal/recoverops` source found in the inventory | Build R1–R4 below |
| Documentation | README still says FaultLab is deferred; HANDOFF contains incompatible completed/unexecuted statements | Establish one current release ledger |

This was a targeted archive inspection for planning, not a fresh full code audit or test run. Do not infer current Git HEAD from a ZIP filename or stale HANDOFF header. Resolve HEAD from the local repository.

FaultLab decision D-011 currently compares a client retry policy: no retry versus one same-key retry. This guide accepts that as the **v1 comparison scope**. Gateway deadline/concurrency hardening from the earlier proposal moves to v1.1. Name the existing experiment “client retry resilience”; it does not establish the benefit of server-side overload protection. Preserve the reported latency tradeoff as well as any success-rate improvement.

## 3. Operating model and persistent memory

Start in `/Users/manuvashistha/Developer/google/sre-portfolio` unless the real local path differs. Read AGENTS, HANDOFF, TASKS, REVIEW, decisions, and this guide; inspect Git status, branch history, available tools, disk, and running lab processes.

Preserve `0fe8b77`, `cad2f7e`, and all existing evidence. Create a continuation branch from the actual latest source containing the work, if necessary; never reset to an older checkpoint just because it appears in chat. Preserve uncommitted work.

Maintain these files:

| File | Responsibility |
|---|---|
| `AGENTS.md` | Stable rules and newly authorized three-project sequence |
| `RELEASE_PLAN.md` | This frozen contract or a link to its committed copy |
| `RELEASE_STATUS.md` | One current table: product/version/status/commit/evidence/blocker |
| `TASKS.md` | Current sprint tasks, criteria, results; historical entries explicitly historical |
| `HANDOFF.md` | One current checkpoint, next command, uncommitted work, runtime state |
| `BACKLOG.md` | Deferred suggestions with priority and target version |
| `CHANGELOG.md` | Delivered behavior and known limitations per release |
| `docs/decisions.md` | Architectural decisions and explicit supersessions |

Statuses: `TODO`, `IN_PROGRESS`, `BLOCKED`, `IMPLEMENTED_UNVERIFIED`, `ACCEPTED`. Do not use DONE for code that has never passed its required live check. Record external review separately as `PENDING` or `REVIEWED`; pending external review does not block autonomous local delivery.

After each coherent task: save code, run the targeted tests, record real exit status, update the handoff, commit locally when coherent. Before a long experiment: save the exact command and run ID. On restart: inspect processes and journals before starting another run. An interrupted test remains unverified unless preserved output establishes completion.

## 4. Stop rules and release discipline

- Explicit `kind-sre-lab` context and `sre-lab` namespace for lab mutations; bind API clients to that configuration too.
- Preserve the configured disk floor and watchdog across every live entry point. Use one clearly stated unit for the threshold. Check after Docker startup and during long runs.
- No automatic cache deletion, broad prune, volume removal, or cluster recreation to bypass a failure. Diagnose and preserve data. Continue independent offline tasks if runtime work is blocked.
- Stop load and reconcile active faults on interruption. Cleanup uses its own bounded context. Report restoration failure honestly.
- Reuse compatible existing tools/images. Pin any new dependencies and update the version record. A failed optional installation does not justify rewriting the stack.
- Test the path the CLI actually uses. Fake-client tests supplement live acceptance; they do not replace it.
- Every evidence bundle identifies the source commit, effective config hash, seed, environment, workload, timestamps and outcome. Distinguish logical operations from HTTP attempts.
- If a bug fix invalidates evidence, repeat only affected scenarios; retain old results with an invalidation reason. Documentation changes alone do not require benchmark reruns.

A product's v1.0 is frozen once its listed gates pass. Later critical bug fixes get v1.0.1 plus targeted regression checks. Optional improvements target v1.1. Do not invent new release blockers during execution.

## 5. Shared lab contract

Reuse Go, PostgreSQL, Prometheus, Grafana and kind. Add Alertmanager for RecoverOps if not already running. No paid service or LLM dependency.

Reservation API:
- POST `/v1/reservations` with required idempotency key, SKU and quantity.
- New commit returns 201; identical replay returns 200 with the same reservation ID.
- Conflicting payload under an existing key returns 409. Invalid requests return a client-error response. Dependency failure must not create a reservation.
- GET reservation, process health, bounded dependency readiness, metrics.
- Unique key plus SQL transaction protects inventory. Commit precedes success response. A lost response can leave a committed operation; same-key retry must not decrement again.

Gateway:
- Stable/candidate routing with compare-and-set version.
- Bounded result/route labels; successful-and-fast histogram bucket at configured threshold.
- Metrics represent client-facing outcomes within the gateway's observable boundary; independent client history covers gateway failure.
- Authenticated admin API; TTL faults expire without the runner.

Load generator:
- Fixed offered-load schedule with explicit drops and bounded drain time.
- Per-run and per-phase operation/key namespaces prevent unintended replay across phases; only deliberate retry uses the same key.
- Separate first attempt and retry identity while maintaining one logical operation identity. If the v1 format preserves only final outcomes, disclose that limitation and do not claim first-attempt metrics.
- Failed/truncated/saturated runs are retained and excluded from performance claims where invalid.

Readiness gate: successful create/read/replay, PostgreSQL invariants, gateway metrics reachable, current context correct, resource checks passing. Do not rebuild the lab if those checks already pass.

## 6. BudgetGuard v1.0 — stabilize, do not rebuild

### Implementation surface

Use existing `cmd/budgetguard`, `internal/budgetguard`, `internal/telemetry`, SLO configs, rules and experiment scripts.

Commands: validate, compile, status, evaluate, replay. Decisions: PASS=0, FAIL=2, INCONCLUSIVE=3, invalid/internal/output failure=1.

For eligible count N, successful count G and successful-fast count F:
- Availability = G/N.
- Joint latency SLI = F/N.
- Bad fraction = 1 - good/N.
- Burn = bad fraction / (1-target).
- N=0, invalid counts or unreliable telemetry must not yield PASS.

Preserve fractional counter increases through evaluation. Use the configured histogram boundary. Validate `0 <= F <= G <= N`; reject nonfinite counts. Keep policy and observed window in output.

Release heuristics remain those in the existing policy: 300-second observation, 1000 requests per slot minimum, candidate error <=1%, error increase <=0.5 percentage points, slow-or-bad <=2%, slow increase <=1 percentage point. Unhealthy stable baseline yields INCONCLUSIVE. Equality passes. These are local release heuristics, not monthly reliability proof.

Telemetry: evaluate at one explicit end time, enforce full interval coverage at both edges and within the interval, <=10% missing and no gap >20 seconds using the configured scrape cadence. Check each required slot/metric population; fresh stable traffic cannot hide a missing candidate. Reject partial responses, NaN/Inf, missing buckets and stale samples on the actual instant/matrix paths used by the evaluator. Validate explicit start/end arguments rather than silently ignoring them.

For each SLI: page using (1h AND 5m at 14.4x) OR (6h AND 30m at 6x); ticket using 3d AND 6h at 1x. Missing-telemetry alerts are separate. No-traffic is different from absent metrics. Demo rules stay separately labeled.

### Fixed v1 acceptance

B-01: build/unit checks and rule syntax/fixtures pass, including thresholds, reset counters, missing series, flat counters, client errors and configured latency bucket.

B-02: one full-window live healthy PASS, error FAIL, slow FAIL and telemetry-loss INCONCLUSIVE, with real CLI exits and saved query evidence. Existing valid runs count.

B-03: reconcile to ten valid full-window repetitions per healthy/error/slow class. Reuse existing compatible three-per-class sets; top up only the missing seven where their configuration/provenance remains valid. Keep native versus kind groups explicit and do not mix incompatible configurations into one accuracy claim. Legacy 80-second runs do not count.

B-04: one working reproduction entry point, honest README, result summary, actual dashboard screenshot and limitation section. Fix the demo if it masks failures or does not generate traffic.

On pass: record BudgetGuard v1.0 and continue immediately to FaultLab. Do not wait for external review or add statistical significance, new dashboards, or cloud deployment.

## 7. FaultLab v1.0 — finish the existing product

### Implementation surface

Reuse `cmd/faultlab`, runner, SQLite journal, scenario schema, injector backends, observer, oracle and comparison scripts.

Commands: validate, plan, run, status, cleanup, reconcile, report; keep direct pod/dep/oracle commands as diagnostic helpers. Supported v1 scenarios: gateway delay, simulated upstream failure, single-pod deletion and bounded dependency outage. Label application-level fault simulation accurately.

Lifecycle: CREATED → PREFLIGHT → BASELINE → INJECTING → OBSERVING → CLEANING → VERIFYING → PASSED/FAILED. Abort/interruption routes to cleanup. Unverified restoration yields CLEANUP_FAILED. Preserve why an experiment failed separately from whether cleanup succeeded.

Persist intent and target before mutation. Lock by target service; record state changes and events transactionally. Reconcile unfinished runs after restart. A killed runner cannot be relied upon for TTL expiry. Pod target must be bound by UID and owner chain to the allowlisted Deployment; never delete its replacement on a retry.

Phases need distinct workload keys. Reusing a seed must not turn all recovery writes into accidental replays. Read the recorded “phase-key” issue in HANDOFF and close it if still present, using a targeted test.

Oracle: read a consistent ledger snapshot after bounded in-flight drain. Check stock conservation, unique effects, acknowledged payload/ID resolution and conflicting-key handling. Reconcile ambiguous operations separately; absent acknowledgement is not proof of absent commit. A same-ID successful replay alone is not evidence that the original response was lost.

### Fixed v1 acceptance

F-01: scenario validation, journal durability, locks, state transitions, idempotent cleanup and reconciliation tests pass.

F-02: live runner demonstrations for expiry, cancellation, killed runner plus independent expiry, restart reconciliation, abort and telemetry loss. Existing compatible evidence counts. A harness success can correctly contain a FAILED or CLEANUP_FAILED experiment outcome.

F-03: pod and dependency faults run through the phased runner with recorded intent/cleanup, plus one controlled commit-succeeded/response-lost oracle case. Reuse the existing runner acceptance bundle after checking provenance and outcome.

F-04: baseline versus one same-key retry, five paired seeds per selected fault (gateway delay and pod deletion), alternating order. That is ten pairs / twenty individual runs for two faults, not twenty pairs. Reuse September 30 results if the effective settings, validity and oracle evidence meet this contract. No mandatory improvement target: unchanged failover results are valid, and worse latency must be published.

F-05: generated comparison report states logical-operation success, goodput, latency, retry amplification, validity, fault/recovery timing and correctness outcomes. If a measurement is unavailable, label it unavailable rather than infer it. A final-outcome-only history cannot establish a full per-attempt latency distribution. Document the client retry experiment's independently sampled retry faults.

On pass: record FaultLab v1.0 and move immediately to RecoverOps. Gateway concurrency controls, richer network simulation and exhaustive history checking are v1.1 work.

## 8. RecoverOps v1.0 — full implementation contract

### Product boundary

A single active Go controller receives alerts, restores one explicitly registered known-good Deployment template, and verifies user-visible recovery. Deterministic policy decides actions. SQLite/PVC stores durable state. Observe mode is default. Enforce mode is restricted to the dedicated lab.

Implement:
- `cmd/recoverops/main.go`: CLI and server entry point.
- `internal/recoverops/{config,webhook,store,controller,policy,kubernetes,verify}.go` plus tests; names may follow existing conventions.
- `migrations/recoverops/`, `configs/policies/lab-rollback.yaml`.
- `deploy/recoverops/`: Deployment, PVC, ServiceAccount, namespaced Role/RoleBinding, internal Service.
- Alertmanager webhook config, demo-only alert, controller metrics, standalone demo script and docs.

Reuse telemetry validation and generic journal helpers where suitable; do not couple to FaultLab private internals or rewrite working libraries just to share them.

### R1: ingestion and durable state

HTTP:
- POST `/v1/alerts`: authenticated Alertmanager webhook; body <=1 MiB; validate each occurrence; commit before returning 202.
- GET `/v1/incidents`: bounded pagination.
- GET `/v1/incidents/{id}`: state, evidence, action and verification.
- POST `/v1/incidents/{id}/cancel`: request cancellation, reconcile in-flight action.
- `/healthz`, `/readyz`, `/metrics`.

CLI: serve, policy validate, incident show, replay, register-good, mode observe/enforce-lab. Define concrete flags in help and test the actual parser.

Tables:
- incidents: id, unique source_key, target_uid, policy_hash, state, timestamps, evidence.
- alert_events: unique delivery identity, occurrence identity, firing/resolved, startsAt/endsAt.
- actions: unique action_key, incident_id, before/desired template hashes, resourceVersion, status, timestamps, error.
- known_good: target UID, canonical template JSON/hash, verification timestamp.
- events: incident_id, monotonic sequence, timestamp, kind and payload; unique incident/sequence.

Occurrence identity combines validated policy/target, alert fingerprint and startsAt. Duplicate delivery must not duplicate an action. Resolve/firing messages arriving out of order cannot reopen a terminal occurrence. Unknown resolved events do not trigger remediation. Periodically scan pending DB rows so losing an in-memory queue does not lose work.

R1 gate: real database reopen tests; duplicate/reordered alerts; failed persistence returns 503; bad auth/body/schema rejected; no cluster mutation.

### R2: policy and observe mode

Policy is pinned to namespace `sre-lab`, Deployment `api-stable`, and known alert/service labels. Action: restore_known_good_template. Limits: one logical action per incident, 600-second cooldown, three actions per hour. Enforce through durable state so restart cannot reset the limits.

Register known-good only through explicit setup after readiness and a healthy traffic observation. Save the entire pod template and bind it to Deployment UID. Strip server-owned metadata; include image, environment, resources and volumes. Never infer a safe revision from “previous” or current existence alone.

Validate target identity, fresh degradation evidence, safe snapshot and limits. Alerts must not select arbitrary resources or supply executable commands. If already known good, do not patch; observe or escalate. Observe mode records a proposal as OBSERVED and cannot count as recovered.

R2 gate: policy refusal tests, observe causes zero writes, snapshot UID mismatch rejected, healthy/stale targets not mutated, cooldown survives restart.

### R3: one rollback with restart reconciliation

States: RECEIVED → VALIDATING → OBSERVING → ELIGIBLE → EXECUTING → VERIFYING → RESOLVED/ESCALATED. Support SUPPRESSED, OBSERVED, CANCELLED and RECONCILING with explicit legal transitions.

Before API mutation, transactionally claim the incident action and persist current UID, resourceVersion, before hash and desired template. Patch with UID/resourceVersion preconditions and replace only the pod template. Do not overwrite replicas or unrelated settings.

On conflict: re-read. A status-only resourceVersion change may get a bounded retry after UID/template checks. A different template means concurrent operator change: escalate rather than overwrite it.

On lost response/restart: GET target. Desired template present means continue verification; old template present permits a bounded conditional retry; third template or new UID escalates. This is one logical desired-state action with possible request retries, not exactly-once delivery. Cancellation cannot undo an already accepted patch; report actual state and stop future actions.

Single controller replica and exclusive local DB/process lock; no high-availability claim. Use least-privilege namespaced access to the named Deployment, plus only reads needed for readiness. No node, exec, secrets-read or wildcard mutation permission.

R3 gate: real kind patch and live reconciliation evidence; fake tests for accepted-but-response-lost, target replacement, operator conflict, API timeout, restart after intent and after application. No repeated rollback loop.

### R4: verify recovery and demonstrate

Require desired template, observed generation, ready/available replicas, and three nonoverlapping ten-second traffic windows. Each needs >=100 eligible requests, >=99% success and >=99% joint fast success under 300 ms. Bound verification to 180 seconds; missing telemetry or timeout escalates without another action. These are lab recovery criteria, not a monthly SLO assertion.

Demo:
1. Healthy baseline, register good template.
2. Apply known bad lab configuration and generate load.
3. Demo-only Prometheus rule triggers Alertmanager webhook.
4. Observe mode records proposal; enforce-lab run restores template.
5. Verify user-visible recovery and save timeline.
6. Replay 100 duplicate deliveries: one logical action.
7. Demonstrate restart reconciliation and concurrent-change refusal.

Run one baseline/controller pilot, then ten paired valid seeds with alternating order. Baseline applies the identical rollback 120 seconds after alert receipt; label it a simulated fixed-delay response, not human on-call performance. Record detection, action, recovery and resolution latency with explicit timestamp definitions. Keep unsuccessful runs censored at the deadline. Report median/range and resolved counts; no invented p95 or zero-risk claim.

R4 gate: genuine Alertmanager-to-controller-to-Kubernetes path, verified recovery, duplicate/restart/conflict acceptance, saved raw events and metrics, paired results, working reproduction docs. Tag RecoverOps v1.0 only when these pass.

## 9. Portfolio v1.0 and agile improvements

After all three are accepted, create one integration demo using their documented interfaces: BudgetGuard evaluates a candidate; FaultLab runs a bounded failure and checks correctness; RecoverOps handles a separate known bad stable deployment. Keep fault ownership clear; do not let the remediation controller interfere with a deliberate FaultLab experiment unless that interaction is the explicit test.

Run one integration smoke and confirm each product still has a standalone entry point. Produce a release matrix with per-product source commit, gates, evidence and limitations. Tag local product releases and the portfolio release only on a clean verified commit; never force-move existing tags. Do not publish automatically.

Then conduct the first cross-project review. Triage findings:

| Priority | Treatment |
|---|---|
| P0/P1: data loss, wrong-target mutation, false PASS from invalid evidence | Immediate patch release with targeted regression |
| P2: incomplete diagnostics, recoverable workflow defect | Next short maintenance sprint |
| P3: polish, new adapters, optional optimization | Version 1.1 backlog |

Suggested v1.1 backlog: independent review fixes; gateway timeout/concurrency profiles; full per-attempt history; statistical canary intervals; stronger YAML validation where needed; richer dashboard presentation; reusable CI integration. Future v2: multiple-controller coordination, cloud environments, broader failure domains. Never claim single-node kind models real regional failure.

Sprint loop: select one small outcome → state its acceptance test → implement → run targeted checks → demo → retrospective → update backlog. Keep work-in-progress to one product milestone. New suggestions do not silently expand an active sprint.

## 10. Copy-paste OpenCode execution prompt

```text
Read SRE_Portfolio_Versioned_Implementation_Guide.md in full and adopt it
as the current release-scope authority. The user authorizes local work
through all three v1.0 products, including RecoverOps.

Inspect the existing source and Git history. Reuse completed work and
valid evidence. Reconcile stale README/HANDOFF/TASKS statements into
RELEASE_STATUS.md. Preserve all baselines and historical results.

Execute automatically in order:
shared readiness -> BudgetGuard v1.0 remaining gates -> FaultLab v1.0
remaining gates -> RecoverOps R1-R4 -> portfolio v1.0 integration.

Use the guide's fixed acceptance gates. After each gate passes, checkpoint
and continue without requesting another plan approval or ChatGPT review.
External review remains separately pending and does not block progress.

Fix defects that violate the frozen contract now. Put optional suggestions
in BACKLOG.md for v1.1. Do not repeatedly rerun valid benchmarks or rewrite
finished components. Do not claim success from mocks or skipped tests.

If runtime checks block live work, save the blocker and continue independent
offline tasks, including the next product's code. Do not falsely mark the
blocked release accepted. Never lower resource thresholds to bypass them.
No unapproved deletion, remote publication, paid deployment or messaging.

Maintain AGENTS.md, RELEASE_STATUS.md, TASKS.md, HANDOFF.md and CHANGELOG.md.
Commit coherent changes locally. Recover sessions from actual files/Git,
not stale chat. At completion provide commits, gate/evidence locations,
limitations and the proposed v1.1 backlog.
```
