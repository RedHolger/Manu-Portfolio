# BudgetGuard run report

run: `results/budgetguard/20261003T045748Z`

| case | decision | reasons | cand eligible/bad/slow | stable eligible/bad/slow |
|---|---|---|---|---|
| healthy.json | PASS | within_gate | 1633/0/0 | 5867/0/0 |
| error.json | FAIL | candidate_error_rate_exceeded,candidate_error_rate_increase_exceeded,candidate_slow_rate_exceeded,candidate_slow_rate_increase_exceeded | 1382/54/54 | 6118/0/0 |
| slow.json | FAIL | candidate_slow_rate_exceeded,candidate_slow_rate_increase_exceeded | 1659/0/1659 | 5840/0/0 |
