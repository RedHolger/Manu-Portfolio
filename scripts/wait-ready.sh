#!/bin/bash
# wait-ready.sh — bounded readiness waits (no infinite loops).
set -euo pipefail
NS=sre-lab
deadline=180
echo "waiting for namespace/$NS rollouts (<=${deadline}s)…"
for dep in api-stable api-candidate labgateway postgres; do
  if kubectl -n "$NS" get deploy "$dep" >/dev/null 2>&1; then
    kubectl -n "$NS" rollout status "deploy/$dep" --timeout="${deadline}s"
  else
    echo "INFO: deploy/$dep not present yet — skipping"
  fi
done
echo "ready"
