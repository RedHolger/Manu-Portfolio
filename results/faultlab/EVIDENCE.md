# F2 live acceptance evidence (kind-sre-lab, 2026-09-28)
1. demo1-invalid: faultSeconds 600 rejected pre-mutation (exit 1, no journal, no faults).
2. demo2-expire: 400ms/50% 20s fault → PASSED, 8 journal events, zero live faults.
3. demo3-sigterm: SIGTERM in OBSERVING → FAILED(cancelled), fault cleared, live empty.
4. demo4-sigkill: SIGKILL in OBSERVING → fault expired via gateway TTL with runner
   dead (Expires shown, then None), traffic flowing (3687 counted).
5. demo4 reconcile: dead run → FAILED(interrupted), 8 events.
6. demo6-abort: conn_fail 100% → FAILED(abort: failure ratio above 50%), cleaned.
   demo6-telemetry: gateway killed mid-run → CLEANUP_FAILED(clear EOF) — restoration
   correctly NOT claimed; fresh gateway restart shows zero faults.
7. Duplicate cleanup: 3× cleanup on terminal runs, all exit 0, live empty.
Journals (j.db) + result.json in each demo dir. Gateway admin was reached via
port-forward (admin Service is cluster-internal).
