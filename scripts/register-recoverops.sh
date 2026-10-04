#!/bin/bash
# Requires healthy steady stable-slot traffic >= 15RPS for at least 40s.
# Existing DB/journal is retained; controller is stopped for explicit registration.
set -euo pipefail
./scripts/require-context.sh
./scripts/disk-floor.sh
K=(kubectl --context kind-sre-lab -n sre-lab)
ORIGINAL=$("${K[@]}" get deployment recoverops -o jsonpath='{.spec.replicas}')
restore() { "${K[@]}" scale deployment/recoverops --replicas="$ORIGINAL"; }
trap restore EXIT
"${K[@]}" scale deployment/recoverops --replicas=0
"${K[@]}" wait --for=delete pod -l app=recoverops --timeout=90s
JOB=$("${K[@]}" create -f deploy/recoverops/job-register.yaml -o jsonpath='{.metadata.name}')
if ! "${K[@]}" wait --for=condition=complete "job/$JOB" --timeout=90s; then
 "${K[@]}" logs "job/$JOB"; exit 1
fi
"${K[@]}" logs "job/$JOB"
# Registration job is preserved as evidence; it is never auto-deleted.
