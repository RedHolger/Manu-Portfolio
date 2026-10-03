# R3/R4 pilot pair (seed r3-live, 2026-10-03)
Definitions: detection = alert startsAt → incident created; action = incident
created → patch accepted (execute); recovery = patch → 3/3 verify windows;
resolution = verify → RESOLVED (pending live resolve write).
Controller (measured): alert 15:13:27Z → incident 15:13:27Z (detection ~0s,
same-process webhook) → execute 15:1x (action, OBSERVED→VERIFYING, UID
70deacef pinned, template-only patch, replicas untouched 2/2) → load
16:15 (800 offered/completed, p99 11.7ms) → verify PASS (3x266 @100/100).
Baseline (simulated fixed-delay, NOT human on-call): identical rollback at
alert+120s; recovery windows assumed same healthy traffic. Controller saves
~120s vs fixed-delay baseline on this seed by construction.
10-pair matrix: PENDING (1 pilot done; 9 pairs deferred — bounded session,
no sleep loops).
