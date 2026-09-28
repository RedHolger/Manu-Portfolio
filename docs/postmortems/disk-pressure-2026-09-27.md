# Postmortem — disk-pressure stall during kind suite (2026-09-27)

## Summary
The in-kind seeded suite was halted at 196MiB host free after one repetition
(`error-222`) stretched a designed 80s run into 35 minutes. No data was lost;
one rep was excluded as contaminated. The lab was stopped, the host recovered
to 8.4GiB, and the suite resumed under a disk floor + watchdog to 10/10/10.

## Timeline (UTC; local IST = UTC+1)
- ~19:50–20:20 — kind deploy, smoke, PG integration 4/4, healthy in-kind
  reps 111–555 all PASS. Host ~498MiB free after image pulls.
- 21:51 — rep `error-222` load starts (rate 70, 80s, 5600 ops planned).
- 22:26 — load still running (35 min); p99 822ms vs ~10ms healthy.
  SIGQUIT dump + kill; evaluator records INCONCLUSIVE on partial data.
- 22:26–22:30 — forensics: host 196MiB, Docker volumes 2.0→2.7GB.
  Suite STOPPED ( Bin: `pkill seeded-suite-kind.sh`, load killed).
- Later — `docker stop` node container (preserved), Docker Desktop quit,
  host back to 8.4GiB. Cluster, volumes, images, evidence intact.
- Resume — floor 2GB + watchdog + per-rep isolation; trial rep ~0 growth;
  suite completed to 10/10/10 + client-error PASS + screenshots.

## First failing behavior (raw evidence)
`results/budgetguard/kind-20260927T204050Z/error-222.jsonl`: 5600 lines over
a planned span 21:51:24 → 22:26:13 (should be 80s); latency p50 5ms / p99
822ms / max 1338ms; codes 201×5563 + 500×37. Decision INCONCLUSIVE
(`telemetry_error`, partial window). Preserved, excluded from benchmarks.

## Diagnosis
- SUSPECTED (not log-proven): host disk exhaustion stalled the Docker
  Desktop VM's filesystem; PostgreSQL WAL writes stalled; request latency
  ballooned; load-generator workers starved (2.7 ops/s vs 70 planned).
- ESTABLISHED from PG logs after restart: unclean shutdown
  ("database system was not properly shut down; automatic recovery in
  progress", zeroed WAL tail record), recovery completed, invariant
  drift=0, keys unique — no data corruption.
- Contributing design gaps (fixed): no disk floor between/during reps,
  unbounded TSDB (now `retention.size=512MB`), rebuild-on-every-run image
  flow (now fingerprint-skipped), 300s windows overlapping prior reps
  (now per-rep PG reseed + gateway/prometheus restart).

## Fix (precise changes)
- `scripts/disk-floor.sh` (tested pass + trip), wired per-rep (floor 2GB).
- `scripts/disk-watchdog.sh`: background poll, kills load + plants STOP.
- `deploy/base/observability.yaml`: `retention.size=512MB`.
- `scripts/build-images.sh`: skip rebuild when binaries newer than sources.
- Suite scripts: per-rep isolation documented in `scripts/seeded-suite-kind.sh`.

## Before / after (same shape: rate 70, 80s, seed varies)
- Before: error-222 — 35 min wall, p99 822ms, INCONCLUSIVE (excluded).
- After: error-777/888, slow-111–555 — ~85s wall each, p99 9–31ms,
  correct FAIL verdicts, all candidate counts ≥1000.

## Action items
- [x] Floor + watchdog + retention cap + rebuild avoidance (this repo).
- [x] Resume suite to valid 10/10/10 under the new guards.
- [ ] Reclaim working space only via explicitly approved paths (Unity /
  Puppeteer caches inspected, awaiting approval; no browser profiles).
- [ ] Grafana sqlite lives on container fs: re-provision dashboards after
  any pod recreation (`scripts/provision-grafana.py`).
