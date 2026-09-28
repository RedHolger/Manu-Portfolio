# Interview notes — answer from the implementation

1. Missing telemetry → INCONCLUSIVE, never PASS: `FetchCounts` errors and
   `LabTelemetryMissing` fires; unknown state must block promotion.
2. p99 chart ≠ availability SLI: percentiles ignore COUNT of failures; our
   SLI is request-count based (`G/N`), joint success-and-speed.
3. Burn 14.4 = exhaust 30d budget in 2d if sustained: (1h window error
   fraction 14.4× the 0.1% budget rate). Fast-burn needs BOTH 1h AND 5m.
4. Commit-then-timeout: client retries SAME key; `TestAmbiguousWriteRetry`
   proves one decrement + original ID (idempotency ≠ exactly-once).
5. Retries don't prove exactly-once: at-most-once effects via key + hash
   compare make retries SAFE; the network can still duplicate delivery.
6. SIGKILL mid-injection: in-process state dies, but gateway TTLs expire
   independently; pod deletion self-heals via Deployment replicas.
7. Readiness ≠ recovery: rollout ready with bad template still fails the
   three-window traffic verification (≥100 req, ≥99% success+fast).
8. No operator-overwrite: conditional patch tests resourceVersion + template
   hash; concurrent change ⇒ escalate, never overwrite.
9. Gateway vs client: gateway counts served requests; loadgen JSONL counts
   logical outcomes incl. transport loss. Disagree ⇒ investigate, not average.
10. kind can't model: AZ/region failure, real network partitions, noisy
    neighbors, cloud IAM, multi-replica consensus.
11. Built vs configured: workload/gateway/loadgen/compiler/evaluator are
    ours; Prometheus/Grafana/Postgres/kind are configured, versions pinned.
12. Reject-resilience evidence: resilient profile losing on ANY paired metric
    (e.g., 503-shed throughput) would force a design revisit — published raw.
