# oracle-check demo-item

window: live · generated: 2026-09-29T22:24:02Z · runs: 1

## oracle

| run | decision | reasons | detail |
|---|---|---|---|
| demo-item | CLEAN |  | committed=12365 acked=1 ambiguous=1 |

## Correctness oracle

- demo-item: **CLEAN**
  - note: op op-ambig-send unacknowledged but key committed (ambiguous, reconciled)

