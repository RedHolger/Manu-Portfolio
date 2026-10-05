# RecoverOps — evidence-bound automated recovery

**Status:** gates R1–R3 **ACCEPTED**, R4 **IMPLEMENTED_UNVERIFIED**
(see [RELEASE_STATUS.md](../../RELEASE_STATUS.md)). The offline suites are
green; the corrected R4 implementation still needs its live redeploy and
paired matrix before it can be called verified.

## What it is

A small service that receives alert webhooks, decides — against a reviewed
policy — whether a *single* bounded remediation is justified, performs it as a
UID-pinned, template-only patch in the dedicated lab cluster, and then proves
recovery from measured traffic before anything is called RESOLVED.

```sh
bin/recoverops serve --db FILE [--addr :8089] [--policy FILE] \
                     [--mode observe|enforce-lab]     # token via RECOVEROPS_TOKEN
bin/recoverops execute  --db FILE --incident ID [--kubeconfig PATH] [--policy FILE]
bin/recoverops verify   --file VERIFY_INPUT.json   # pure check, exit 2 when not recovered
bin/recoverops policy validate --policy FILE
bin/recoverops incident show --db FILE --id ID [--events]
bin/recoverops replay   --db FILE --policy FILE --file BATCH.json
bin/recoverops register-live --db FILE --policy FILE --prometheus URL
bin/recoverops mode [--db FILE] [observe|enforce-lab]
```

`observe` records what *would* happen and never touches the cluster — that is
the default and the safe mode for anyone reading this.

## State machine (one transition table, enforced in one place)

```
RECEIVED → VALIDATING → OBSERVING → ELIGIBLE → EXECUTING → VERIFYING → RESOLVED
    ↘ OBSERVED (proposal recorded, observe mode)
any non-terminal → SUPPRESSED | CANCELLED;  EXECUTING/VERIFYING → RECONCILING | ESCALATED
RESOLVED, ESCALATED, SUPPRESSED, CANCELLED are terminal: no outgoing edges.
```

- Transitions are validated by a single table (`legalTransitions`); a late
  webhook for a terminal occurrence cannot reopen it.
- Every write happens inside the same transaction as its event append, and
  execution intents are durable: a crash between steps is reconciled, not lost.

## Safety properties

- **Policy-gated.** Action allow-list (`restore_known_good_template` only),
  cooldown ≥ 600 s, at most 1 incident per incident and ≤ 3 per hour —
  validated by `recoverops policy validate`; limits are re-checked at
  execution time, not just at proposal time.
- **Single-flight, UID-pinned.** At most one active execution per target UID;
  the patch is bound to a recorded UID and a known-good template snapshot,
  template-only (no replicas mutation), with a compare-and-swap on
  resource-version so a concurrent edit aborts the action.
- **Cluster scope.** `enforce-lab` requires the `kind-sre-lab` context; the
  service needs no broad RBAC, and observe mode performs zero cluster writes.
- **Token required.** `RECOVEROPS_TOKEN` must be set (never committed).
- **Verification before RESOLVED.** Recovery is only accepted with the desired
  template present, observed generation advanced, ready replicas satisfied,
  and three non-overlapping 10 s traffic windows each with ≥ 100 eligible
  requests, ≥ 99 % success and ≥ 99 % joint fast-success (< 300 ms), bounded
  to 180 s. Missing telemetry or a timeout escalates — it does **not** trigger
  another action.
- **Refusals are evidence.** Cooldown, budget, restart-collision and
  template-mismatch refusals are recorded with reasons rather than retried.

## Reproduce

```sh
go test ./internal/recoverops/ ./cmd/recoverops/     # offline suites
go build -o bin/ ./cmd/...
bin/recoverops policy validate --policy <FILE>
bin/recoverops mode observe                              # zero cluster writes
```

## Evidence

- Live kind rollback (R3):
  `results/recoverops/r3-live-20261003T151252Z/` — webhook → OBSERVED/PROPOSED,
  UID-pinned template-only patch, replicas intact, restart persistence,
  100-duplicate batch produced a single action.
- R4 cycle evidence preserved: `results/recoverops/r4-am-20261003T154519Z/`,
  `r4-demo-20261003T153515Z/`, `pairs/pair-02/` (both arms RESOLVED with
  passing verification windows), plus the recorded refusals.
- Offline suites cover reopen, duplicate/reordered delivery, 503, auth/schema/
  body rejection, no-Kubernetes-dependency, refusal/cooldown/budget/mismatch,
  restart persistence and conflict/timeout handling.

## Known limitations

- R4's corrected implementation (durable intents, UID-bound execution,
  autonomous verification) is **offline-tested only**: live redeploy,
  migration/registration, Prometheus/Alertmanager verification and the paired
  matrix are still open, so no live claim is made for it here.
- Historical RV1/RV2 results are historical; they must not be mixed with the
  newer schema-3 evidence.
- The remedy set is deliberately tiny (one template restore). This is a
  safety argument, not a missing feature: an autonomous system in a lab should
  have one bounded verb before it earns more.
