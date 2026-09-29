# External code review — BudgetGuard correctness findings

Source: independent review of code, scripts, tests, and saved request logs.
The reviewer could not rerun Go tests (no Go in the review environment).
Received 2026-09-28. Nothing below is reduced to titles: each finding keeps
its reported mechanism, why it matters, and the fix verification.

Scope directive attached to the review: keep `0fe8b77` as the preserved
baseline, create a correctness-fix branch, require targeted regression
tests per fix, rerun affected experiments with a genuinely complete
observation interval, preserve existing results as preliminary
short-window evidence, keep FaultLab deferred until gaps are closed.

## High priority

### H1 / BG-01 — Full-window coverage is never checked
**Reported:** `FetchCounts` checks only the newest service-wide timestamp.
The suite restarts Prometheus, generates 80 seconds of traffic, then
reports a 300-second window. The reviewer verified approximately 80 seconds
in both native `healthy-11` and kind `healthy-111` request logs. These runs
do not satisfy the required complete observation window.
**Why it matters:** every acceptance decision taken on those runs evaluates
a fraction of its stated window; PASS/FAIL verdicts are not full-window
evidence.
**Verified in code:** `evaluate.go` computed `start` then discarded it
(`_ = start`); no coverage query existed anywhere.
**Fixed (commit `cad2f7e`):** `CheckCoverage` in
`internal/budgetguard/coverage.go` — per-slot range query, step 15s,
≤10% missing samples, no gap >20s, first/last sample within one step of
the window edges; wired into `FetchCounts` before any counts are read.
Regression tests: full pass, 80s-in-300s rejection, 105s-gap rejection,
empty-window rejection (`coverage_test.go`).

### H2 / BG-02 — Live queries bypass telemetry validation
**Reported:** `Client.Query` ignores partial-result warnings; its sample
parser accepts NaN/Inf. The rejection tests exercise `QueryRange`, which
the evaluator never calls. Passing tests do not establish that the actual
release gate rejects incomplete or nonfinite telemetry.
**Verified in code:** `Query` returned after status check only; `QueryRange`
had the checks. The shared helper then written also caught a real bug:
fixed-size `[2]any` zero values made every matrix response fail validation.
**Fixed (`cad2f7e`):** `checkEnvelope` + `checkSamples` in
`internal/telemetry/client.go`, called by `Query`, `QueryRange`, and the
new `QueryMatrix`; vector and matrix forms both validated. Regression
tests: instant-path warning, NaN, and +Inf rejection (`client_test.go`).

### H3 / BG-03 — Watchdog does not reliably stop the suite
**Reported:** the watchdog kills `labload-bin run`, but `resume-kind.sh`
launches `go run ./cmd/labload` (temp binary path). A STOP breaks one
class's loop, then execution proceeds to another class. The protection may
leave load running or start another experiment after a disk-floor breach.
**Verified in code:** `resume-kind.sh` used `go run`; class blocks had no
inter-block STOP guards; `pkill -f 'labload-bin run'` misses `exe/labload`.
**Fixed (`cad2f7e`, hardened later):** watchdog kills all three launcher
forms; `halted()` guards in `scripts/lib.sh` wrap every class block in
both kind suites; `scripts/selftest.sh` proves kills + STOP planting with
`exec -a` fake processes. Debugging the selftest also exposed that the
watchdog read its floor from `$2` while callers passed `MIN_GB` by
environment — now accepts both (`${MIN_GB:-${2:-2}}`).

### H4 / BG-04 — Destructive commands use the current context
**Reported:** suite scripts issue database `TRUNCATE` and deployment
mutations without explicit context enforcement. A namespace named `sre-lab`
alone does not ensure the commands target the dedicated cluster.
**Verified in code:** no script or binary checked the context.
**Fixed (`cad2f7e`):** `scripts/require-context.sh` (dedicated
`kind-sre-lab`, `SRE_CONTEXT` override for tests) wired into both kind
suites, the acceptance script, and the demo script; `faultlab run`
verifies via a kubectl lookup (`defaultCheckContext`). Tested with stub
kubectl (accept/refuse/override) in `selftest.sh`.

### H5 / BG-05 — Load generator extends runs under saturation
**Reported:** the scheduled-operation index advances only when enqueueing
succeeds. Dropped launches are retried on subsequent ticks, so a configured
duration is not a fixed experiment duration under overload, and
offered/launched/dropped accounting can become inconsistent.
**Verified in code:** `for scheduled < n` with `scheduled++` only on
successful enqueue.
**Fixed (`cad2f7e`):** every fired tick counts toward `n` (enqueued or
dropped); a far deadline remains only as a stall guard; new `Truncated`
flag; invariant `Offered == Launched + Dropped`; drop rate computed over
offered with a zero guard. Regression tests: saturation ends on time with
`Truncated=false` + `Valid=false`, context-cancel truncates, clean-run
accounting (`loadgen_test.go`).

### H6 / BG-06 — Configuration and numeric contracts diverge
**Reported:** the evaluator always queries `le="0.3"` despite accepting
other thresholds, so valid configuration changes evaluate the wrong latency
objective. It rounds Prometheus increases to integers and discards the
estimated-count flag, so rounding can change boundary decisions.
**Verified in code:** literal `le="0.3"` in `fastQ`; `int64(x+0.5)` before
gating; `Estimated` never set.
**Fixed (`cad2f7e`):** bucket label rendered from `cfg.LatencyThreshold`;
`Counts` is float64 end to end with gating on raw fractions;
`DecideCounts` renders integer display counts and sets `Estimated` from
extrapolation flags. Regression tests: 0.5-threshold query capture,
20.4999/2000 → FAIL (rounded would PASS), estimated flag set
(`evaluate_test.go`).

### H7 / BG-07 — Reproduction commands mislead
**Reported:** `make test-rules` still uses the obsolete command form
(`promtool test --test-file`, removed in Prometheus 3.x);
`make test-workload` matches none of the actual workload test names; the
smoke command accepts completed attempts even when every request fails.
The advertised acceptance commands do not reliably demonstrate behavior.
**Verified in code:** all three as reported.
**Fixed (`cad2f7e`):** `test-rules` runs `check rules` + `test rules FILE`;
`test-workload` matches the real test names; smoke requires ≥1 2xx
(`Successful` counter, `runSmoke` returns codes). Regression tests:
smoke fails against an all-500 server, passes when healthy
(`cmd/labload/main_test.go`).

### H8 / BG-08 — Compiler is incomplete
**Reported:** it generates neither the required long-window ticket alerts
nor latency's 6h/30m alert branch. Successful rule syntax tests do not
prove the promised alert policy was implemented.
**Verified in code:** one combined availability alert (page severity
covering both windows) and a page-only latency alert; no 3d/6h anywhere.
**Fixed (`cad2f7e`):** workbook-shape split — FastBurn/page (1h+5m) and
TicketBurn/ticket (6h+30m, 3d+6h) per SLI, plus a 3-day 0.5%-error ticket
fixture proving ticket-without-page (`rule-tests.yaml`, now 10/10).

## Additional concrete defects

### BG-09 — `writeResult` ignores file-write errors
**Reported:** it can report a successful decision while saving nothing.
**Fixed (`cad2f7e`):** returns error; all three CLI callers propagate
(exit 1). Round-trip + unwritable-path tests (`cmd/budgetguard/main_test.go`).

### BG-10 — Missing-bucket test exits before the bucket
**Reported:** `TestMissingBucketUnknown` returns freshness `1000`, so it
exits at the stale-telemetry check before reaching the missing bucket.
**Fixed (`cad2f7e`):** fake answers freshness 5 and serves full coverage
matrices, so the test reaches the empty bucket query and asserts the
`fast`-query error.

## Rerun requirement (from the review)
Rerun affected experiments with a genuinely complete observation interval
(rate 25 × 330s in-kind, coverage gate enforced); preserve existing
results as preliminary short-window evidence rather than counting them as
full-window acceptance. Done: `results/budgetguard/kind-fullwindow-*/`
9/9 (healthy/error/slow), zero INCONCLUSIVE; SUMMARY.md carries both
labels. FaultLab stays deferred until gaps are closed (they are; review
of the fixes themselves remains pending).
