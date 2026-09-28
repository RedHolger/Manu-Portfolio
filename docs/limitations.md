# Limitations (read before citing any number)

1. Single-node kind: pod/app failures only. NOT zone/region loss, NOT
   packet-level partitions. Gateway delay/loss are application-layer
   simulations — label them as such in every demo frame.
2. A 5-minute canary NEVER proves a 30-day 99.9% SLO. `history_complete`
   is always false in the lab. Burn-rate alerts (monthly budget) and canary
   gates (regression) are different jobs; this repo implements both without
   conflating them.
3. Counts from `increase()` are fractional (extrapolation) — preserved and
   labeled `estimated`, never silently coerced to integers.
4. Demo alert windows (30s/10s) are demo-only; production compliance uses
   the multiwindow rules. Never mix the files.
5. Simulated 120s manual baseline (future RecoverOps work) is a fixed-delay
   script, NOT measured human on-call behavior.
6. Results describe THIS laptop/cluster/workload — not Google-scale production.
7. `excludeResults: [client_error]` is configured but VACUOUS in this lab:
   the load generator sends only valid requests, so no client_error series
   exist. Recording rules and FetchCounts use total counts; the exclusion
   would need `result!="client_error"` selectors in a workload that emits
   them. SLI math itself (sli.go) operates on the already-excluded population.
