# Demo 1 — invalid scenario rejected before any mutation
- Command: `faultlab run --scenario /tmp/bad-fault.yaml` (faultSeconds 600 > 120).
- Result: `invalid scenario: faultSeconds 600 > 120 bound`, exit 1.
- Gateway faults after: null (none active).
- Journal: no j.db created (validation precedes CreateRun/lock/mutation).
