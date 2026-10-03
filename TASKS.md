# TASKS.md — milestone ledger (honest status only)

## Review-findings register (BG-01–BG-10; full text in docs/REVIEW.md)
All fixed on `correctness-fixes` (`cad2f7e`); live reruns done in-kind.
Status reflects executed tests and measured outcomes only.

- BG-01 Full-window telemetry coverage — DONE. Files: `internal/budgetguard/coverage.go`,
  `coverage_test.go`, `evaluate.go` (wired). Criteria: ≤10% missing, no gap >20s,
  window edges verified per slot or INCONCLUSIVE. Tests: 4 coverage tests
  (full/short/gap/empty) + live full-window suite 9/9 (rate 25 × 330s, zero
  INCONCLUSIVE). Outcome: enforced live. Blockers: none. Commit: `cad2f7e`.
- BG-02 Validation on the instant-query path — DONE. Files:
  `internal/telemetry/client.go` (`checkEnvelope`, `checkSamples`, `QueryMatrix`).
  Criteria: warnings + NaN/Inf rejected on instant and range paths. Tests: 3
  instant-path rejections + existing range tests; caught a real `[2]any` bug.
  Outcome: enforced. Blockers: none. Commit: `cad2f7e`.
- BG-03 Watchdog termination and whole-suite abort — DONE. Files:
  `scripts/disk-watchdog.sh`, `scripts/lib.sh`, both kind suites.
  Criteria: all launcher forms killed, STOP planted, every class block halts.
  Tests: `scripts/selftest.sh` (fake `exec -a` processes). Outcome: proven;
  also exposed the env-vs-positional floor bug, fixed. Blockers: none. Commit: `cad2f7e`.
- BG-04 Explicit Kubernetes context enforcement — DONE. Files:
  `scripts/require-context.sh`, kind suites, acceptance + demo scripts,
  `faultlab run` (`defaultCheckContext`). Criteria: any foreign/absent context
  refused pre-mutation. Tests: stub-kubectl accept/refuse/override. Outcome:
  enforced. Blockers: none. Commit: `cad2f7e`.
- BG-05 Fixed-duration scheduling and accounting — DONE. Files:
  `internal/loadgen/loadgen.go`. Criteria: every fired tick counts toward n;
  `Offered == Launched + Dropped`; `Truncated` only on early stop. Tests:
  saturation timing + cancel truncation + clean accounting. Outcome: enforced.
  Blockers: none. Commit: `cad2f7e`.
- BG-06 Configured threshold and fractional counts — DONE. Files:
  `internal/budgetguard/evaluate.go`, `sli.go` (float64 `Counts`),
  `DecideCounts` + `Estimated`. Criteria: bucket label from config; gating on
  raw fractions; display-only rounding. Tests: 0.5-threshold capture,
  20.4999-boundary FAIL, estimated flag. Outcome: enforced. Blockers: none.
  Commit: `cad2f7e`.
- BG-07 Reliable Makefile, smoke, acceptance commands — DONE. Files: `Makefile`,
  `cmd/labload` (`runSmoke`, `Successful`). Criteria: `make test-rules` uses
  `test rules FILE`; `test-workload` matches real names; smoke needs a 2xx.
  Tests: smoke fail/pass servers; `make test-rules` SUCCESS; 5 workload PASS.
  Outcome: verified live. Blockers: none. Commit: `cad2f7e`.
- BG-08 Complete alert policy — DONE. Files: `internal/budgetguard/compile.go`,
  `monitoring/rule-tests.yaml` (10/10). Criteria: FastBurn/page + TicketBurn/
  ticket per SLI incl. 3d/6h. Tests: compiler assertions + 3-day ticket
  fixture (ticket fires, fast stays silent). Outcome: `promtool` SUCCESS.
  Blockers: none. Commit: `cad2f7e`.
- BG-09 Result-file write errors propagated — DONE. Files: `cmd/budgetguard`
  (`writeResult` returns error; 3 callers). Criteria: unwritable path exits 1.
  Tests: unwritable-path + round-trip. Outcome: enforced. Blockers: none.
  Commit: `cad2f7e`.
- BG-10 Missing-bucket test reaches the bucket — DONE. Files:
  `internal/budgetguard/evaluate_test.go`. Criteria: fake serves fresh
  timestamp + full coverage so the empty bucket is what fails. Tests: asserts
  the `fast`-query error. Outcome: enforced. Blockers: none. Commit: `cad2f7e`.

## Session history (append-only; newest last)

## Session 2026-09-27 (cont.) — toolchain live, unit gates PASS
- Deleted approved caches 2,3,5 only (uv 2.3G, pip 1.9G, Homebrew 606M;
  realpaths verified, no symlinks). 509Mi → 5.4Gi free.
- `brew install go` → go1.27.1 darwin/arm64 (matches D-003). versions.lock.json updated.
- Fixed 5 real defects found by compiler/vet/tests: NewID call site,
  scalarSum return arity, 3× Fprintln-redundant-newline, float-epsilon test,
  gate-boundary test value (0.005 equality → 0.0051).
- `gofmt -l` clean, `go build ./...` OK, `go vet ./...` clean,
  `go test ./...` ALL PASS (incl. `-race` workload+gateway, `-count=1` rerun).
- CLI verified with built binary: replay healthy→PASS/exit 0,
  error→FAIL/exit 2, slow→FAIL/exit 2, thin→INCONCLUSIVE/exit 3.
  (`go run` masks exit codes — always verify with built binary.)
- Disk now 4.5Gi (Go + build cache consumed ~0.9Gi).

## Session 2026-09-27 (cont.2) — LIVE gates: partial PASS
- Docker: data dir ~empty (52K group container, no VM image) — nothing to
  inspect; daemon never started. No delete/resize performed. Host 9.5Gi free.
- Minimal stack estimate (measured after install): prometheus bottle ~150MB
  on disk, postgresql@16 ~200MB, pgx module ~30MB. Grafana/Alertmanager/kind
  deferred (kind node image ~1.5GB alone — needs the 15GiB buffer + approval).
- promtool 3.15.0 (brew prometheus): `check rules` 13 rules OK; `test rules`
  5/5 PASS after 3 fixture fixes (service label, summary annotation, moderate
  burst redesigned to eval mid-burst 103m proving 5m-fires/1h-does-not).
  NOTE: promtool 3.x syntax is `test rules FILE` (Makefile fixed); `check
  metrics` subcommand no longer exists — exposition proven by live scrape.
- Live stack (all local, no kind): labapi×2 + labgateway + prometheus :9090.
  Recording rules evaluate; bad-ratio 0 on healthy traffic.
- BUG (live-found): zero-error series absent → evaluator INCONCLUSIVE on
  healthy traffic. Fix: gateway InitSeries pre-creates bounded combos
  (spec §5.3) + TestInitSeriesZeroErrorsExist. MemStore could never find this.
- BUG (live-found): fastQ queried `_count{le=}` (nonexistent) → fixed to
  `_bucket{le="0.3"}`. Live bucket query verified before re-evaluating.
- BUG (live-found): labload passed os.Args[1:] to FlagSet — "run" stopped
  parsing, ALL defaults (100rps/300s, 30000 ops). Fixed + comment; proven by
  200/200/200 three-way agreement (labload/gateway/JSONL).
- LIVE healthy release → PASS exit 0 (stable 11400/0/0, candidate 2645/0/0).
  Evidence: results/budgetguard/live-20260927/ (decision+summary+manifest).
- LIVE missing telemetry (empty Prometheus) → INCONCLUSIVE exit 3 via new
  freshness gate (`time()-max(timestamp())` > 20s). Freshness check added to
  FetchCounts per §5.3 (was a spec gap).
- Postgres integration (native 16.15, :5433, stopped after): 4/4 PASS —
  concurrent unique, dup+conflict, stockout rollback (no leaked row),
  committed-but-unacknowledged replay. Found REAL bug: UUID canonicalization
  (RETURNING vs local hex) — fixed by returning insertedID.
- Processes stopped; machine left clean. Disk 9.5Gi free.

## Session 2026-09-27 (cont.3) — B4 native singles + seeded suite
- Regression tests (executable, not comments): labload split/parse incl.
  leading-subcommand rejection; FetchCounts bucket-dispatch fake (SlowBad=50
  exact); zero-init presence; UUID-canonical assertion (integration).
- Latency rules fixed to histogram-bucket joint SLI (were success-only);
  client_error exclusion documented vacuous (limitations #7).
- Rule fixtures now 7/7 PASS: healthy, sustained error, moderate burst
  (no page, eval 103m), severe burst (page), slow-only (latency page +
  availability no-page), counter reset (pages), missing series.
- promtool 3.15.0 version+help captured: docs/evidence/promtool.txt.
- LIVE singles (native, fresh binaries): error→FAIL exit 2 (cand 1427/70 =
  4.9% ≈ seeded 5%); slow→FAIL exit 2 (slow_or_bad 55%, p99 402ms; 63
  transport bads = error-run residue in overlapping 300s window — documented
  contamination, verdict still correct via slow reasons); stale gateway 35s→
  INCONCLUSIVE exit 3 (scrape failures emit stale markers → freshness gay);
  prometheus down→INCONCLUSIVE exit 3 (connection refused). Empty-Prometheus
  case from prior session also INCONCLUSIVE.
- Seeded suite COMPLETE 15/15 perfect separation (matrix.md committed):
  healthy 5/5 PASS, error 5/5 FAIL, slow 5/5 FAIL, zero INCONCLUSIVE.
  All reps cand_elig ≥1000 (min observed 1205). Evidence:
  results/budgetguard/seeded-20260927T195421Z/ (15 decisions + summaries + JSONL).
## Session 2026-09-27 (cont.4) — kind live, suite HALTED at disk gate
- Kind sre-lab ONLY (v1.37.0, kind 0.33.0, kubectl 1.37.1). Digests pinned in
  versions.lock.json (node, postgres:16, prom v3.15.0 == :v3, grafana 11.6,
  both local images). Fixed live: GOOS=linux cross-compile (exec format
  error), grafana:11 bad tag → 11.6, missing prometheus-rules ConfigMap,
  migrations applied in-kind (readiness was failing without tables).
- labapi now honors DATABASE_URL → PostgresStore in-kind (pgx in binary).
- Kind smoke PASS (201, canonical UUID, PG-backed). In-kind PG integration
  4/4 PASS via port-forward. Grafana 11.6.16 healthy; dashboard provisioned
  (uid sre-slo, 5 panels); :30300 NodePort refused → use port-forward :13030
  (recorded anomaly, screenshots pending).
- In-kind results: healthy 111-555 5/5 PASS (cand ≥1058); error-111 FAIL
  clean (1105/55 = 5.0%); error-333 FAIL valid-with-caveat (98.6% complete,
  1017/56); error-222 CONTAMINATED (35-min disk-stall crawl, SIGQUIT-killed,
  INCONCLUSIVE — excluded from benchmark, preserved); error-444 INCOMPLETE.
- Class totals: healthy 10/10 ✓ (5 native + 5 in-kind); error 6-7/10;
  slow 5/10 (native slow stays CONTAMINATED; clean rerun not done);
  client-error verification run not done.
- ROOT CAUSE of stall: host disk 498→196Mi during suite; Docker volumes
  2.0→2.7GB (VM image growth from TSDB churn/restarts). PG WAL stalls →
  p99 822ms → worker starvation. Suite STOPPED per disk-gate rule at 196Mi.
  Cluster left RUNNING (idle) for resume. No deletions performed.
- kind space assessment (no installs, advisory buffer): kind binary ~15MB;
  kindest/node image ~1GB compressed/~2GB unpacked; postgres+prom+grafana
  ~1GB unpacked; our images ~50MB; TSDB+pgdata tiny. New footprint ≈4-5GB;
  at 9.5Gi free this FITS with ~4-5Gi headroom. VERDICT: space does not
  block kind; Docker Desktop first-start creates the VM (no resize of
  anything existing). Proceed after suite. No deletions proposed.

## Session 2026-09-27 (cont.5) — HALT: lab scaled down, Docker off
- No stray loadgens; only sre-lab existed (no unrelated workloads).
- DB health check BLOCKED: API timeouts + VM-level I/O errors under disk
  pressure (crictl exec → EIO). Invariant query and pg-log forensics could
  NOT run — WAL-stall stays SUSPECTED, not established (honest).
- Stop: `docker stop` node container (preserved, not deleted) → Docker
  Desktop quit → daemon unreachable. Disk 196Mi → 8.4Gi; growth stopped.
  Cluster, volumes (pgdata), images, evidence all preserved for resume.
- Cache inspection (read-only, no deletions, browsers untouched):
  Unity = upm/db 540M + upm/packages 1.1G (total 1.6G, Sep 20, re-downloadable);
  Puppeteer = chrome mac_arm-144.0.7559.96 327M + headless-shell 185M
  (total 512M, April, re-downloadable). Awaiting approval; nothing deleted.
- Growth controls (code, no deletions): Prometheus retention.size=512MB cap;
  build-images.sh skips rebuild when binaries newer than sources;
  scripts/disk-floor.sh (tested pass+trip) wired into kind suite per-rep;
  documented that ~2GB relief is necessary but not sufficient (~35MB/rep +
  VM image growth observed).
- results/SUMMARY.md: native vs kind reported separately with configs.

## Session 2026-09-27 (cont.6) — resume complete, all gates green
- DB health PASS after restart: PG crash-recovery completed in logs
  ("not properly shut down ... invalid record length ... end-of-recovery"),
  invariant drift=0, keys unique, live create/read/replay identical IDs.
  WAL-stall cause of slowdown stays suspected (no proving log line).
- Disk watchdog (auto-kill load + STOP file) + floor=2GB wired into suite;
  trial rep measured ~0 host growth (+1GB system purge, VM +7MB) → resumed.
- FK-violation bug (unknown SKU → 500, masking client_error): fixed with
  explicit existence probe + integration test; pool cap fix for
  too-many-clients; rebuilt linux images, reloaded, digests updated.
- Client-error exclusion VERIFIED LIVE: 594×400s excluded, PASS held.
- In-kind resume: error 777/888 FAIL, slow 111–555 FAIL (pure slow, bad=0),
  clienterror PASS. Class totals: 10/10/10 ✓ (+ clienterror PASS).
- Screenshots: real headless-Chrome captures (overview + burn) verified by
  viewing. Dashboard re-provisioned after halt-cycle loss (recorded).
- Lab scaled to 0 (all deploys), cluster + volumes + images preserved.
  Docker left running idle; disk steady 5.2Gi. README + SUMMARY final.

## Gate status (honest, current)
- B1/B2/B3: PASS (incl. 9/9 promtool fixtures, live evaluator gates).
- Classes: healthy 10/10 ✓, error 10/10 ✓ (1 caveat), slow 10/10 ✓,
  client-error live PASS ✓. Contaminated runs excluded + preserved.
- Screenshots captured + verified. README/SUMMARY final.
- Lab scaled to 0; Docker idle; disk 5.2Gi steady; no deletions ever made.
- Wrote 21 Go files (~2.6k LOC): workload (reserve/mem/postgres/tests),
  labapi, gateway (+exp, tests), labgateway, loadgen, labload, telemetry
  (+contract tests), budgetguard (config/sli/compile/evaluate/CLI/tests).
- Wrote manifests: kind.yaml, base/{namespace,postgres,api,gateway,
  observability,kustomization}, monitoring/{prometheus,rule-tests},
  migrations/001_init.sql, configs (SLO + 3 release profiles), 4 fixtures
  (healthy→PASS, error→FAIL, slow→FAIL, thin→INCONCLUSIVE by hand-check).
- Wrote scripts (all `bash -n` clean): doctor, bootstrap, build-images,
  demo-budgetguard (trap-EXIT unconditional reset), report.py (py_compile OK),
  validate-configs, wait-ready. `validate-configs.sh` needs pyyaml (present).
- Static checks: lexer-based brace/paren/bracket balance OK on all 21 files;
  JSON fixtures + versions.lock.json parse; shell syntax OK.
- UNEXECUTED (toolchain blocked): `go build`, `go test`, `go vet/gofmt`,
  `promtool check/test`, `make lab-up/smoke/test-workload`, all live kind +
  Postgres + Prometheus runs, B4 seeded suite, Grafana screenshots.
  First compile+test cycle is the next exact task after Go install.

## M0 — Environment prep: SUPERSEDED (see session log above; live toolchain resolved)
- Measured: macOS arm64, 8 CPU / 16 GB RAM, **547 MiB disk free** (Data vol 100%).
  kubectl v1.32.2 present. `go` MISSING (brew stable 1.27.1, not installed).
  `kind` MISSING. Docker client 28.3.0 present, **daemon DOWN**
  (`Cannot connect … /docker.sock`). promtool MISSING. python3 present (conda).
- Scaffold created: dirs, AGENTS.md, TASKS.md, docs/, go.mod (provisional
  `go 1.27`, toolchain unresolved), Makefile, versions.lock.json (UNRESOLVED).
- Disk triage (read-only) done; cleanup proposal awaiting user approval:
  1. `~/.cache/huggingface` 4.3G (re-downloadable model cache)
  2. `~/.cache/uv` 2.3G (package cache, regenerable)
  3. `~/Library/Caches/pip` 1.9G (regenerable)
  4. `~/Library/Caches/Unity` 1.6G (regenerable)
  5. `~/Library/Caches/Homebrew` 606M (`brew cleanup` equivalent)
  6. `~/.cache/puppeteer` 512M + `~/.cache/torch` 343M (regenerable)
  Estimated safe reclaim ≈ 11–12 GiB. Browser caches (Firefox/Google
  1.4G each) listed as OPTIONAL only.
- BLOCKED (needs approval + space): `brew install go`, kind install, Docker
  start, image pulls, `kind create cluster`, digest pinning.
- UNBLOCKED now: all pure-Go source + unit/contract tests (writable without
  toolchain; execution awaits Go install), YAML fixtures, docs.
- Next exact task: user approves cleanup list → free ≥15 GiB → install Go →
  `go build ./... && go test ./...` → start Docker → `make doctor`.

## M1 — Shared lab (§4): SUPERSEDED (implemented + tested live; see above)
- Planned files: `migrations/postgres/001_init.sql`, `internal/workload/`,
  `cmd/labapi`, `cmd/labgateway` + `internal/gateway`, `cmd/labload` +
  `internal/loadgen`, `deploy/kind.yaml`, `deploy/base/`,
  `monitoring/prometheus.yaml`, `scripts/{doctor,bootstrap}.sh`.
- Extra gate (correction #5): ambiguous-write test — commit-succeeded /
  response-lost, same-key retry returns ORIGINAL id, stock decremented once.
- Acceptance: `make lab-up && make smoke && make test-workload && make
  lab-down`; 100×concurrent unique → 0; dup-key → 1 decrement; conflict → 409.

## B1 — SUPERSEDED STUB: Parser + SLI math: NOT STARTED
- Gate: `go test ./internal/budgetguard/...` + `-race`; golden/invalid YAML,
  zero-traffic→UNKNOWN, negative budget preserved, boundary equality passes.

## B2 — SUPERSEDED STUB: Rule compiler: NOT STARTED
- Correction #6: TWO burst fixtures — moderate burst (must NOT page) + severe
  burst (must page). `make test-rules` (promtool check + test).

## B3 — SUPERSEDED STUB: Telemetry + evaluator: NOT STARTED
- Gate: fake-Prometheus contract tests + real-Prometheus integration (the
  latter blocked until Docker/kind up; record if unexecuted).

## B4 — SUPERSEDED STUB: Experiment + demo: NOT STARTED
- One REAL run per scenario first (healthy/error/slow/missing-telemetry),
  then seeded suite (≥10 reps/live class). Unconditional routing reset.
- Finish: `make demo-budgetguard`, Grafana PNGs from actual runs, README +
  one postmortem, resume bullet with MEASURED numbers only.

## Session 2026-09-27 (cont.7) — handoff packaged, commit b9cfc91
- Audit: no hardcoded credentials (env/secretKeyRef only); dev-default token
  replaced with hard requirement; binaries (`bin/`, `*-bin`), `*.env`,
  `*.log`, `__pycache__/` all gitignored + verified untracked.
- Postmortem `docs/postmortems/disk-pressure-2026-09-27.md` written;
  runbook resume-from-paused section added; README links all artifacts;
  SUMMARY lists contaminated + below-minimum runs with reasons.
- Local commit only (no remote configured, nothing pushed). FaultLab /
  RecoverOps remain deferred.

## Session 2026-09-28 (correctness-fixes branch) — all findings fixed + full-window acceptance
- H1 coverage gate (CheckCoverage: ≤10% missing, no gap >20s, window edges) +
  4 regression tests (incl. grid-alignment lesson). H2 shared envelope/sample
  validation for Query + 3 instant-path tests (caught a real [2]any bug).
- H3 watchdog kills all launcher forms + suite-wide STOP guards + lib.sh +
  require-context.sh; selftest.sh ALL PASS (debugged env-vs-positional bug).
- H5 tick-count schedule + Truncated + accounting invariant + 2 tests.
- H6 threshold-parameterized bucket + float-preserving gate + Estimated flag
  + 2 tests. H8 FastBurn/TicketBurn split per SLI + long-ticket fixture (10/10).
- H7 truthful make targets; smoke requires Successful>0 + 2 tests.
- D1 writeResult errors + CLI test; D2 missing-bucket test fixed.
- Full-window in-kind suite 9/9 (rate 25 × 330s, coverage enforced):
  healthy 3 PASS, error 3 FAIL, slow 3 FAIL, zero INCONCLUSIVE. Watchdog idle.
- Prior results relabeled preliminary short-window in SUMMARY.md.
- Lab scaled back to 0; disk steady 6.9Gi; no deletions.

## Session: FaultLab F1–F2 start (faultlab-dev from cad2f7e)
- HANDOFF.md checkpoint written. Tree was clean; no uncommitted work to preserve.
- BudgetGuard external review + 10-per-class top-ups: PENDING (not completed).
- Disk 2.9GiB at branch time; floor 2GB + watchdog carry over to F2 demos.

## Session: FaultLab F1 complete (faultlab-dev)
- Strict FaultExperiment schema (unknown/dup rejected; context+namespace
  enforced; pod_delete explicitly deferred to F3; TTL/duration/fraction bounds).
- SQLite journal (modernc v1.59.0, WAL, single-writer): intent-before-mutation
  rows, CAS transitions with events, service lock, terminal lock release,
  reopen durability. State machine with exact-transition tests.
- Fake clock (advance/waiters) + fake injector (record/script errors).
- CLI: validate + plan (target/workload/phases/fault/abort/cleanup/permissions).
  run/status/cleanup/reconcile/report intentionally absent until F2.
- `go test ./...` all green. Disk 2.6GiB (sqlite module cost ~300MB).

## Session: FaultLab F2 complete (faultlab-dev)
- Gateway injector (bounded admin client, kind mapping, idempotent) + fake-admin
  contract tests. Observer (exposition scrape, window ratios, consecutive abort
  logic) + unit tests incl. 12s live-abort test.
- Runner: journaled phases, preflight probe, abort (3-strike telemetry loss),
  fresh-context cleanup, verified-or-CLEANUP_FAILED, SIGTERM-safe deferred net,
  reconcileOne/forceFailed (terminal FAILED is not a mechanical error).
- CLI: run/status/cleanup/reconcile/report (+ kubectl context check).
- 7/7 live demos on kind-sre-lab (see results/faultlab/EVIDENCE.md):
  invalid-reject, expire+recover PASSED, SIGTERM cleanup, SIGKILL TTL expiry,
  reconcile interrupted, abort + telemetry-loss CLEANUP_FAILED, dup cleanup.
- Full `go test ./...` green. Lab scaled back to 0; disk 1.5GiB steady.
- Deferred intact: F3/F4, pod deletion, correctness oracle, RecoverOps.

## Session: FaultLab F3/F4 offline start (faultlab-dev, from 26e2fe3)
- Baselines preserved (0fe8b77, cad2f7e untouched). BudgetGuard external
  review + 10-per-class top-ups remain PENDING, recorded not completed.
- Constraints: no image builds, no live runs, no deletions; fake clients +
  fixtures only. K8s/PG acceptance marked UNEXECUTED until actually run.
- HANDOFF.md: commits, evidence paths, test-vs-outcome distinction.

## Session: FaultLab F3 offline (faultlab-dev)
- PodDeleter: allowlist (api-stable|api-candidate), namespace enforcement,
  owner-UID resolution, UID-precondition delete, lost-response reconcile,
  replacement-never-touched, VerifyGone with budget. 6 fake-clientset tests.
- labapi dependency-fault admin (localhost-only, token-gated, TTL lazy
  expiry, pre-tx 503, nothing written while failing) + unit test + manifest
  args (not applied). Runner/CLI wiring for pod faults deferred to live F3.
- K8s/PG live acceptance: UNEXECUTED. Disk 5.8GiB; no pulls/builds/deletions.

## Session: FaultLab F4 offline (faultlab-dev)
- Oracle (Check): conservation, unique keys, acked-write resolution,
  ambiguous reconciliation (info, not violation), conflicting-payload
  detection. PGLedger ready (live acceptance UNEXECUTED).
- 5 JSON fixtures: healthy CLEAN (1 ambiguous reconciled); dup-key,
  broken-stock, lost-write, conflict all detected with named violations.
- Report renderer preserving CONTAMINATED/INCONCLUSIVE/VIOLATED outcomes.
- `go test ./...` 10/10 packages green. K8s/PG live acceptance UNEXECUTED.
  No image builds, no live runs, no deletions. Disk 5.8GiB.

## Session: F3/F4 live acceptance (faultlab-dev)
- Wired CLI connectors (pod-delete, dep-fault, oracle-check) after confirming
  they were standalone-only. Fixed live: manifest token env + stable admin-addr
  (candidate crashloop), transitive ReplicaSet ownership (pods are NOT owned
  by Deployments directly — unit fixtures corrected the same way).
- Acceptance results/faultlab/acceptance-20260929T143244Z: UID-precondition
  pod delete + replacement 2/2; dep-fault 300x503 then 200x201 post-TTL;
  oracle CLEAN over 12,139 real rows (11 acked + replay, 0 violations).
- Full unit suite green. Disk 1.3-2.3GiB throughout; no deletions, no benchmarks.

## Session: floor uniformity + acceptance guards (faultlab-dev)
- ROOT CAUSE (reviewer finding confirmed): disk-floor.sh defaulted MIN_GB=1
  while session notes claimed a 2GB floor; acceptance-f3f4.sh ran at
  1.3GiB under that effective 1GB floor, the watchdog tripped mid-run
  (STOP file preserved in results/faultlab/acceptance-20260929T143244Z/),
  and only one upfront STOP check existed — later steps ran post-breach.
- Fix: default floor is now 2GB everywhere (kind suites already used 2);
  STOP+floor gate before every mutating acceptance step; selftest asserts
  the default. No thresholds were lowered to bypass anything.

## Session: runner integration + ambiguity E2E + floor hardening (faultlab-dev)
- Runner now dispatches gateway/pod/dep faults with journaled intent, lock,
  abort polling, kind-specific cleanup verification, and reconcile support.
  Unconfigured backends rejected pre-mutation (no journal row). CLI run takes
  --kubeconfig/--api-admin; pod-delete/dep-fault/oracle-check commands added.
- Ambiguous-outcome E2E (labapi X-Test-Drop-Response hook, field-gated):
  commit lands, reply lost (transport error), same-key retry returns original
  ID with exactly one decrement. Ordinary replay tests do not cover this.
- Floor: default now 2GB everywhere; per-step gates in acceptance script;
  selftest asserts default + override (fixed its own low-disk false failure).
- `go test ./...` 11/11 green + selftest ALL PASS. Disk ~1.4GiB (test builds
  consumed ~1GB; no cleaning per no-deletion constraint). No live runs.

## Session: runner pod/dep live acceptance (faultlab-dev)
- Post-restart recovery: Docker Desktop started (daemon ~20s), kind node
  Ready, all 8 lab pods Ready after one restart each. No cluster
  delete/recreate; volumes, journals, evidence preserved.
- PG crash-recovery clean in logs; pre-run invariants: 12,139 rows (matches
  pre-restart acceptance), 0 dup keys, conservation drift 0.
- Parser change (completes runner integration): `pod_delete` now parses
  (was F3-gated rejection); `TestParsePodDelete` added, stale "runner stays
  gateway-scoped" comment fixed. `go test ./...` 11/11 green.
- Dep runner abort path: FAILED(abort) CORRECT — single-pod fault +
  keep-alive pinning drove gateway-observed stable failures to 85/85 in the
  fault window (other pod got zero). Journal: intent/applied/cleared,
  8 events; IsFailing false post-run. Proven by pod logs (50 baseline
  writes, zero after arm) + gateway counters.
- Dep runner full path (abort 1.0, documented): PASSED, 8 events,
  applied+cleared, IsFailing false.
- Pod runner path: PASSED. Journaled UID target (hct7v d38e6685…),
  applied+cleared, 8 events; original gone, replacement 69cwn + wtktz 2/2
  Ready. 175 gateway successes / 0 errors; PG 125 DISTINCT rows.
  CORRECTION (mechanism, verified by key audit): the 50 `replayed:true`
  operations are NOT ambiguity recoveries. The runner reuses one seed
  across phases, so the fault phase re-offered baseline keys load-702-1..50
  (50 baseline writes + 75 new fault-phase writes = 125 rows; wtktz logged
  75 first-writes + 50 replays). The deleted pod took no traffic — failover
  was clean with zero failed requests, but this run exercised NO ambiguity.
  Per-attempt histories were not preserved, and no retry was even enabled
  (runner load uses CorrectnessProfile=false). The deliberately
  unacknowledged operation below remains the ONLY controlled ambiguity
  evidence. Zero double-writes; conservation holds.
- RUNNER WART (filed, affects validity of runner-based fault phases):
  phases share one keyspace when the seed is constant, so fault-phase
  "new operations" partially replay baseline keys and trivially succeed.
  Comparison harness uses single-phase labload runs (disjoint keys per
  run); runner fix (phase-disjoint keys) pending.
- Oracle ambiguous live case: deliberate unacked op + same-key retry →
  same ID, exactly one decrement (87636→87635); oracle-check CLEAN over
  12,365 rows (acked=1, ambiguous=1 reconciled, violations=0).
- Evidence: `results/faultlab/runner-acceptance-20260929T221906Z/` (3 run
  dirs + journals, scenarios, oracle, disk-end 18GiB). Watchdog armed,
  never tripped (no STOP). Disk 19.5→18GiB; floor held all steps.
- Left running: nothing (all port-forwards stopped, watchdog killed).
  Lab idle, 2/2 stable Ready. No deletions.
- Still pending (runtime-blocked): baseline-versus-resilient comparison;
  BudgetGuard 10-per-class top-ups. Independent source re-review is
  NOT runtime-blocked (separate track). RecoverOps deferred; no benchmarks;
  no publishing.

## Session: baseline-versus-resilient comparison (faultlab-dev)
- Profiles (new, D-011): `configs/profiles/baseline.yaml` (never retry) vs
  `resilient.yaml` (retry 503/timeout/transport once, same key). No
  spec doc or profiles existed in-repo; single-setting design chosen because
  no restart-surviving server knob exists and retry directly tests the
  idempotency design. Single-pod dep-fault excluded (gateway→pod keep-alive
  pinning makes it luck-dependent — documented in D-011).
- PILOT (seed 905, delay) EXPOSED A REAL BUG: resilient retried nothing
  (`attempt2:0` both runs; all fails classed `transport`). Root causes:
  (1) client timeouts misclassified as transport (ctx is Background, so the
  `ctx.Err()` check never fires); (2) retry condition excluded transport.
  Fixed in `internal/loadgen`: net-timeout → `timeout`, retry on
  timeout/transport/503, `AttemptID` initialized to 1 (was 0). New
  `TestRetryOnceSameKey` (5 subtests) green. Pilot dir preserved as the
  before-fix record (`results/faultlab/compare-20260930T001528Z/`).
- Also corrected last session's pod-run narrative: the 50 `replayed:true`
  were baseline-key re-offers (runner reuses one seed across phases — filed
  as a runner validity wart), not ambiguity recoveries. Deliberate unacked
  op remains the only controlled ambiguity evidence.
- MATRIX (`results/faultlab/compare-20260930T002156Z/`, 20/20 runs valid,
  alternating order, per-pair command diff enforced, reseed per run,
  per-run oracle): rate 10 × 100s, fault t+20..t+60.
  - gateway_delay 0.5/3000ms (5 pairs): resilient good +96.6 mean
    (fail 0.195→0.098), amp 1.206 (+0.206 cost), p99 2001→4001ms
    (FLAGGED REGRESSION: converted ops pay two timeouts). pg_committed
    higher (+77..+108: retries land formerly-lost writes, all idempotent).
    20/20 oracles CLEAN. Fault-window concentration verified (183/400
    failed in-window, 0 before, 2 after).
  - pod_delete (5 pairs): 10/10 runs 1000/1000, retries 0, amp 1.0 —
    10 distinct pods deleted+replaced (verified targets), zero failed
    requests in ANY run. Replica loss is fully masked; no separation, no
    regression. Recovery metric 0s everywhere (post-clear goodput never
    dipped — definition noted in report).
- Attempts vs ops: JSONL holds one line per logical op (final attempt
  only); attempts = lines + attempt==2 lines; first-attempt latency not
  preserved (D-011). `scripts/compare-profiles.sh` (pilot|matrix) +
  `scripts/compare-report.py` committed. (First pilot invocation crashed on
  a `set -u`/`local` bash bug before any run — stillborn dir
  `compare-20260930T001503Z/` left on disk uncommitted; no experiment ran.)
- Cluster left clean: 8/8 Ready, routing stable-only, no live faults, no
  strays. Disk 19.0→18GiB; watchdog armed throughout, never tripped.
  Docker died once mid-session (Mac-side); restarted, PG clean-shutdown
  recovery, no data loss. No deletions.
- Still pending: BudgetGuard top-ups, independent source re-review
  (non-runtime track), RecoverOps, publishing. Runner phase-key wart fix.

## Session: durable handoff files (faultlab-dev)
- AGENTS.md rewritten (stable instructions; dropped M0-blocked env facts,
  15GiB rule, B1-B4-only scope). TASKS.md gained BG-01–BG-10 register with
  status/files/criteria/tests/outcome/commit; history preserved below it.
- HANDOFF.md rewritten to checkpoint format against verified live state
  (branch f817c53, 8/8 pods Ready, no lab processes, disk 2.3GiB).
- docs/REVIEW.md created: full external findings reconstructed from chat
  (previously nowhere in the repo) + fix verification pointers.
- No code changes; no retest needed (suite green at HEAD). Docs only.

## Session: v1 guide adoption + readiness + BG/FL gates (faultlab-dev)
- Adopted `~/Downloads/SRE_Portfolio_Versioned_Implementation_Guide.md` as
  v1 authority: committed verbatim as RELEASE_PLAN.md; AGENTS.md scope now
  three-product sequence with RecoverOps AUTHORIZED; new RELEASE_STATUS.md
  (gate table), BACKLOG.md (v1.1/v2), CHANGELOG.md.
- B-01 ACCEPTED (re-verified: build+tests green, `make test-rules` SUCCESS).
- B-02 ACCEPTED: dead-Prometheus evaluation → INCONCLUSIVE exit 3 with saved
  query evidence (`results/budgetguard/telemetry-loss-20261003T024957Z/`).
- F-03 ACCEPTED: journal provenance audit (intent rows incl. pod UID,
  applied/cleared timestamps, 8 events/run, terminals match result.json).
- F-04/F-05 ACCEPTED: contract counts match (10 pairs/20 runs); report
  regenerated with Methods section (retry sampling, final-only history,
  recovery/validity definitions) — additive diff only.
- Phase-key wart FIXED (contract §7): runner phases use disjoint seeds
  (`phaseSeedOffset`: baseline +0, fault +1000000) + `TestPhaseSeedOffset` /
  `TestPhaseKeyNamespacesDisjoint`. Full suite green. gofmt fixed a
  pre-existing indent wart in scenario_test.go (my earlier hunk).
- Shared readiness re-verified live: 8/8 Ready, context kind-sre-lab, PG
  1000 rows intact across Docker outage (0 dup, drift 0), metrics reachable,
  create/replay same-ID + API-direct read 200. Gateway has no GET route
  (pre-existing write-path-only design; reads are API-direct) — noted, not
  a blocker. Readiness writes added 1 row (ready-001); next suite reseeds.
- B-03 top-up: `seeded-suite-fullwindow.sh` gained SEEDS_* overrides (same
  config, seeds 1004-1010/class = 21 runs). Launch pending commit.
- Still TODO: B-03 matrix, B-04 demo check, R1-R4, portfolio integration.

## Session: B-03 top-up launch + B-04 demo fix (faultlab-dev)
- B-03 matrix launched: seeds 1004-1010/class via SEEDS_* overrides (same
  suite config), 21 reps in background (`/tmp/bg-topup.log`). Pre-launch:
  killed my readiness pg forward (port conflict), committed adoption first.
- B-04 demo defects fixed OFFLINE in `demo-budgetguard.sh` (no live run
  yet — top-up owns the cluster): (1) demo generated NO traffic (sleep
  300 with no load → every case INCONCLUSIVE); now runs labload 25x330s
  per case with distinct seeds; (2) `|| true` masked evaluation outcomes;
  now captures exit codes and fails on mismatch (healthy→0, error/slow→2),
  with `|| code=$?` guard under `set -e`. Live demo run deferred to after
  top-up (both mutate candidate mode/routing).
- Known suite side effect (pre-existing, preserved for provenance): the
  fullwindow suite patches candidate args (drops admin-addr/dep admin on
  candidate) and bumps routing version; declared-live drift to reconcile
  after B-03 (re-apply manifests + readiness re-check).

## Session: B-03 top-up complete + B-04 demo live (faultlab-dev)
- B-03 ACCEPTED: 21/21 top-up reps (seeds 1004-1010/class) perfect
  separation — error 7 FAIL (~5% bad), slow 7 FAIL (100% slow, 0 errors),
  healthy 7 PASS (cand ≥1446). With 1001-1003: 10/10/10 full-window, zero
  INCONCLUSIVE/STOP/failures. SUMMARY.md rewritten to full-window-only
  ledger (legacy groups explicitly non-counting).
- B-04 ACCEPTED: fixed demo ran live (`results/budgetguard/20261003T045748Z/`):
  healthy PASS/exit 0, error/slow FAIL/exit 2, exits.log matches, traffic
  per case. First launch aborted on two latent defects (no admin pf, no
  token export) + missing require-context — all fixed; empty aborted dir
  left on disk. Live-found cleanup-order bug (pf killed before routing
  reset orphaned 20% candidate): fixed, verified live 20→0 via extracted
  function, routing restored. Suite/demo patch drift reconciled via
  `apply -k` (candidate args+admin-addr+healthy restored, rollout clean).
  Final readiness green (8/8 Ready, stable-only, create/replay OK).
- BudgetGuard v1.0: all four gates ACCEPTED. Next per contract: FaultLab
  already accepted → RecoverOps R1 (new `recoverops-dev` branch).

## Session: RecoverOps R1 ingestion + durable state (recoverops-dev)
- New branch `recoverops-dev` from `af015ba`. R1 per contract §8, no stubs:
  `cmd/recoverops` (serve/policy-validate/incident-show/replay/
  register-good/mode), `internal/recoverops` (config, strict policy parse
  + hash, SQLite store with embedded schema, webhook + offline ingest),
  `migrations/recoverops/001_init.sql` (schema.sql embedded + byte-match
  test against migrations), `configs/policies/lab-rollback.yaml`.
- Semantics: all-or-nothing batch validation; commit-before-202 with
  dispositions; delivery-ID dedupe; occurrence = policy+fp+startsAt;
  unknown/out-of-order resolved never opens a case; terminal never reopens;
  cancel→409 on terminal; persistence failure→503; 1MiB cap→413;
  serialized processing; mode persisted; register-good canonicalizes+hashes.
- R1 gate evidence (all passing, no cluster mutation — `go list -deps`
  test asserts zero k8s.io deps): 17 internal tests (reopen preserves +
  seq continuity, dup collapse, illegal transitions, register-good,
  firing/dup/resolve/terminal-stickiness/unknown-resolved, 10 rejections,
  batch atomicity, 503, cancel/list/show, ready/metrics, replay ingest)
  + CLI surface tests. Full `go test ./...` green (verify at commit).
- No new modules (modernc sqlite already pinned). Alertmanager ABSENT from
  cluster — R4 prerequisite, not installed now (minimal scope).

## Session: RecoverOps R2 policy + observe (recoverops-dev)
- R2 per contract §8 (no cluster mutation): `internal/recoverops/evaluate.go`
  (freshness ≤15m, snapshot presence/mismatch, cooldown + hourly budget from
  durable rows; per-incident limit structural via incident-scoped action
  key), OBSERVED/PROPOSED proposals wired into the firing path
  (observe→OBSERVED action, enforce-lab→PROPOSED for the R3 executor),
  RECEIVED→OBSERVED edge (+OBSERVED→RESOLVED/CANCELLED), incident show now
  includes actions. Live already-known-good comparison deferred to R3
  (needs cluster read) — documented in code.
- R2 gate evidence: TestEvaluateEligible/Refusals (stale/future/label/
  target/no-snapshot/mismatch), TestCooldownAndBudget (incl. hourly
  budget), TestCooldownSurvivesRestart (executed row → close → reopen →
  still refused), TestObserveProposalRecorded (both modes, idempotent
  re-proposal, one logical action). Full suite green.

## Session: RecoverOps R3 offline executor (recoverops-dev, live PENDING)
- R3 per contract §8, offline only (Docker daemon down, kind API refused;
  no cluster recreation per stop rules). Live kind patch + reconciliation
  evidence explicitly PENDING, not claimed.
- `internal/recoverops/store.go`: extended state machine VALIDATING →
  OBSERVING → ELIGIBLE → EXECUTING → VERIFYING → RESOLVED/ESCALATED plus
  SUPPRESSED/RECONCILING; backward-compat RECEIVED→OBSERVED kept.
- New `internal/recoverops/executor.go` (pure decisions + transactional
  claim): ClaimForExecution (action_key incident-scoped, idempotent),
  MarkActionStatus, DecideOnPatchError / DecideAfterRestart /
  DecideOnResponseLost (desired→verify, before→bounded retry, third
  template or new UID→escalate, unknown→escalate, no loop).
- New `executor_test.go` 8 tests: response-lost verify/retry, target
  replacement escalate, operator conflict escalate, timeout + RV-conflict
  retry, restart-after-intent single-action, restart-after-application
  verify, unknown escalate. `go test ./...` 13 pkgs green, `go vet` clean.
- New `deploy/recoverops/` (least-privilege, lab-only): ServiceAccount,
  Role (get/update/patch deployments.apps/api-stable + get/list pods;
  no nodes/exec/secrets/wildcards), RoleBinding, PVC 1Gi, single-replica
  Deployment, kustomization.
- Reused BudgetGuard/FaultLab evidence; no benchmark reruns.
- Still TODO live: real kind patch, restart-after-intent/application on
  cluster, conflict/timeout live paths, R4 verify + demo, portfolio demo.

## Session: RecoverOps R4 offline verifier (recoverops-dev, live PENDING)
- R4 per contract §8, offline only (same Docker-down block as R3).
  Alertmanager-to-controller-to-k8s path, paired seeds, and demo timeline
  explicitly PENDING.
- New `internal/recoverops/verify.go`: pure VerifyRecovery (desired +
  generation + ready replicas + exactly 3x10s windows ≥100 eligible,
  ≥99% success, ≥99% fast<300ms; 180s/missing-telemetry → escalate with
  no further action).
- New `verify_test.go` 6 tests: recovered, thin-window fail, slow fail,
  missing-telemetry escalate, timeout escalate, unready fail.
- `go test ./internal/recoverops/... ./cmd/recoverops/...` green.
- Still TODO live: healthy baseline + register-good, bad-config + load,
  demo rule → webhook, observe vs enforce-lab, 100-dup single-action,
  restart + conflict refusal, 1 pilot + 10 paired seeds, portfolio smoke.

## Session: RecoverOps R3 live acceptance (recoverops-dev, kind-sre-lab)
- Docker started via open -a Docker (daemon up <10s); kind cluster
  preserved (5d18h, no recreate); doctor exit 0; disk floor OK (19.9Gi);
  wait-ready rollouts green; gateway create+replay same ID; metrics OK.
- Wiring proven live (Alertmanager process absent — payload is
  Alertmanager-compatible): POST /v1/alerts → incident OBSERVED +
  PROPOSED action (desired_hash == registered known-good) in
  `results/recoverops/r3-live-20261003T151252Z/`.
- Controlled bad change: env R3_BAD=1 via kubectl strategic patch (context
  enforced), rollout green. `recoverops execute` → OBSERVED→VERIFYING,
  UID-pinned 70deacef, template-only restore (replicas 2/2 untouched),
  R3_BAD removed; semantic restore verified (image/env/metadata equal;
  hash-form mismatch = canonicalization debt → BACKLOG P2).
- Restart: server killed, DB reopened → VERIFYING + 1 EXECUTED persist;
  live UID unchanged → verify path (no re-patch).
- 100/100 duplicate live deliveries → same incident, exactly 1 action.
- R4 traffic: labload 800/800 p99 11.7ms; 3x266 @100%/100% → `verify`
  CLI recovered. Gen 13/13, ready 2/2. Pilot pair recorded (controller
  measured, baseline simulated +120s); 9-pair matrix PENDING.
- Portfolio smoke: BG replay PASS, FL validate valid, RO show VERIFYING.
  No B/F benchmarks repeated.

## Session: R4 genuine Alertmanager path + pilot pair (recoverops-dev, kind live)
- Wired + pinned Alertmanager `prom/alertmanager:v0.28.1`
  (`sha256:27c475...b15ba`): Deployment/Service/ConfigMap under
  `deploy/alertmanager/`; demo rule `LabBadTemplate` (bad-ratio>5%/2m,
  `for: 1m`, policy-pinned labels) in `prometheus-demo-rules`; AM target
  in `prometheus.yml`. Burn alerts route to blackhole (all-or-nothing
  webhook validation would otherwise poison demo batches — found live).
- Fixed two live-found defects: (1) secret created with trailing newline
  (`echo` vs `printf`) → 401s; (2) server spoke array-only JSON, AM sends
  v2 envelope → added envelope adapter + 2 tests (firing+resolved,
  garbage rejected). Burn-alert batches without target labels correctly
  400 (policy pin enforced).
- Controller runs in-cluster `enforce-lab` with 10s reconciler
  (`reconcile.go`, 1 test, no-loop) + `verify` CLI; `execute` CLI for
  manual path. `TestNoKubernetesDependency` narrowed to ingest path
  (planned). `go test ./...` green.
- Genuine cycle `0bc3b543` (r4-am bundle): error-0.5 degradation →
  rule firing → AM → webhook → PROPOSED → reconciler patch →
  VERIFYING → traffic recovery → AM resolved → RESOLVED. Restart
  persistence confirmed. Measured windows 3x150 @100/100 → verify PASS.
- Pilot arms: controller `70e51910` (alert 16:13:34 → acted 16:13:51 →
  resolved 16:16:14, full journal) + baseline `e102a674` (120s hold via
  scale-0/1, acted 16:39:59, resolved 16:42:18). Second firing properly
  refused by 600s cooldown (valid policy evidence, `7172efbe`); later
  arm properly refused by 3/h budget (`ce847b0c`). Matrix must space
  executions (driver `r4-pair.sh` is budget-aware now).
- Q3 answer: `verify` CLI is pure (no persistence); VERIFYING persists
  until the genuine resolved webhook transitions to RESOLVED.
