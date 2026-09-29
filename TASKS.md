# TASKS.md — milestone ledger (honest status only)

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
