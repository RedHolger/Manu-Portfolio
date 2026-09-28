# Architecture — shared lab + BudgetGuard

```
k6/loadgen (open loop, JSONL) ──POST /v1/reservations──▶ labgateway :8080
        │                                                    │ route 20% CAS
        │                                                    ├─▶ api-stable :8081 ──▶ postgres :5432
        │                                                    └─▶ api-candidate :8081 ──▶ postgres
        │                                              metrics: lab_requests_total{service,slot,route,result}
        │                                              + duration histogram (0.3s bucket) ──▶ Prometheus :9090
        └─ correctness oracle (op IDs, same-key retry)      │  recording rules (compiler output)
                                                            ▼
                        budgetguard evaluate ──▶ PASS(0)/FAIL(2)/INCONCLUSIVE(3) + results/<run-id>/
```

Gateway-observed SLI is authoritative (an API crash cannot hide from its own
metrics). Loadgen JSONL is the correctness oracle. BudgetGuard never mutates
the cluster; the demo script owns routing and ALWAYS resets it (trap EXIT).
