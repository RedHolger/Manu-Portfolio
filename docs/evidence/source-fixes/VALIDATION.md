# Source-fix validation — 2026-10-03

Executed in the Linux source workspace using Go 1.27.1, against the corrected
source. These are implementation checks, not live experiment outcomes.

| Check | Result |
|---|---|
| `go build ./...` | PASS |
| `go vet ./...` | PASS, no findings |
| `go test -race ./...` | PASS: 13 tested packages; labgateway has no direct tests |
| `gofmt -l cmd internal` | Clean |
| Python report unit tests | PASS: 2 tests; incomplete/mismatched/old-schema/nonfinite evidence excluded |
| Python driver compilation | PASS |
| `bash -n` | PASS: 21 shell scripts |
| YAML parsing | PASS: 33 deployment/config/rule files; syntax only |
| Shell selftest | INCOMPLETE, exit 77: context/STOP/disk checks pass; process-matching check skipped |
| `git diff --check` | PASS |
| Uploaded admin-token value scan | No matches in packaged source files |

The shell environment exposes virtual shell PIDs that do not match `ps`'s process
table. `pkill` also reports `fatal library error, lookup self` here. The watchdog
process-matching test cannot establish its required behavior in this environment;
the selftest now reports that limitation as exit 77 instead of a false pass.
Run `make test-scripts` on the actual host before relying on the older watchdog.
The new Python experiment driver owns subprocess handles and checks disk directly.

New regression coverage includes mode/proposal/UID authorization, lost-response
reconciliation without a second patch, operator edits, bounded persisted attempts,
queued-proposal cooldown, atomic cancellation, generation/UID/rollout verification,
missing buckets/stale telemetry, fractional/impossible counts, process lock
exclusion, isolated load keys with identical fault draws, and output-write errors.

Not executed: promtool, Docker image build/loading, kind, PostgreSQL integration,
Alertmanager delivery, upgraded registration/migration against the live journal,
additive inventory top-up, corrected measured pairs or combined demo. Their current
status is IMPLEMENTED_UNVERIFIED/BLOCKED as recorded in RELEASE_STATUS. Existing
results in the upload are historical evidence, not substitutes for these checks.
