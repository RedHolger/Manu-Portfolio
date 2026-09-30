# Design decisions

- D-001 (2026-09-27): Scope = shared lab + BudgetGuard B1–B4. FaultLab /
  RecoverOps deferred. Rationale: fastest interview signal; approved scope.
- D-002: Language = Go for all binaries; Python stdlib only for offline
  `scripts/report.py` aggregation. Rationale: spec §1 + static binaries for
  kind images.
- D-003: Go version provisional `1.27` (brew stable 1.27.1 observed
  2026-09-27). CONFIRM after install via `go version`; update go.mod +
  versions.lock.json then. Toolchain not yet resolved — honest placeholder.
- D-004: Single-node kind cluster `sre-lab`, namespace `sre-lab`. Tests pod/
  app failures only, NOT AZ/region loss. All claims labeled accordingly.
- D-005: Gateway-observed SLI is authoritative for release decisions;
  loadgen JSONL is the correctness oracle. Rationale: API crash must not
  vanish from its own metrics (spec §4.2).
- D-006: Latency SLI is joint success-and-speed (failed-even-if-fast = bad,
  no double-count). N=0 / missing / nonfinite → UNKNOWN, never PASS.
- D-007: Demo alert rules live in a SEPARATE file from production multiwindow
  rules and are labeled demo-only in every demo/README frame.
- D-008: Routing cleanup unconditional (correction #4): reset candidate → 0
  on PASS, FAIL, INCONCLUSIVE, error, timeout, SIGINT/SIGTERM — fresh bounded
  context for the cleanup itself.
- D-009: Burst fixtures are a PAIR (correction #6): moderate (no page) +
  severe (page). A single "short burst" fixture is rejected as ambiguous.
- D-010: No cleanup deletion without explicit per-path approval. `make doctor`
  missing-item output is diagnostic, not a gate pass.
- D-011 (2026-09-29): FaultLab baseline-versus-resilient compares ONE client
  setting — `retryAmbiguousOnce` (labload `--correctness-profile`):
  baseline retries never, resilient retries 503/timeout once with the same
  idempotency key. Rationale: server-side knobs that survive a restart do
  not exist in the lab (gateway 5s transport is hardcoded; labapi modes are
  faults, not hardening), while retry policy is fully wired, needs no image
  rebuild, and directly tests the idempotency design (interview notes 4-5).
  The harness builds both commands from shared variables and fails if
  anything but the profile flag differs. labapi single-pod dep-fault
  excluded as a comparison fault: gateway→pod keep-alive pinning makes its
  effect luck-dependent (total outage iff the pinned pod is faulted, and a
  retry over the still-pinned connection cannot convert it). Selected
  faults: gateway_delay (independent per-attempt draw; measures gain vs
  amplification) and pod_delete (real failover; measures recovery).
  labload JSONL records one line per logical op (final attempt only), so
  attempts = lines + retried lines and first-attempt latency is not
  preserved — reported honestly as logical-op latency + retry rate.
