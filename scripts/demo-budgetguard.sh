#!/bin/bash
# demo-budgetguard.sh — B4 standalone demo (unconditional routing cleanup).
# Flow: healthy PASS → error FAIL → slow FAIL → report. Every exit path
# resets candidate traffic to zero with a FRESH bounded context (correction #4).
set -euo pipefail
ADMIN="${ADMIN:-127.0.0.1:8082}"
PROM="${PROM:-http://127.0.0.1:9090}"
CFG="configs/slos/reservations.yaml"
TS="$(date -u +%Y%m%dT%H%M%SZ)"
OUT="results/budgetguard/$TS"
mkdir -p "$OUT"

cleanup() {
  # Fresh bounded context: plain curl with --max-time (never the cancelled ctx).
  curl -s --max-time 10 -X PUT "http://$ADMIN/admin/routing" \
    -H "Authorization: Bearer ${LAB_ADMIN_TOKEN:-}" \
    -H 'Content-Type: application/json' \
    -d '{"version": 999999, "candidatePercent": 0}' >/dev/null 2>&1 || true
  # Version-guarded reset: fetch current version, then CAS to 0.
  VER=$(curl -s --max-time 10 "http://$ADMIN/admin/state" \
    -H "Authorization: Bearer ${LAB_ADMIN_TOKEN:-}" 2>/dev/null | python3 -c 'import sys,json;print(json.load(sys.stdin).get("routingVersion",0))' 2>/dev/null || echo "?")
  if [ "$VER" != "?" ]; then
    curl -s --max-time 10 -X PUT "http://$ADMIN/admin/routing" \
      -H "Authorization: Bearer ${LAB_ADMIN_TOKEN:-}" \
      -H 'Content-Type: application/json' \
      -d "{\"version\": $VER, \"candidatePercent\": 0}" || true
  fi
  echo "cleanup: candidate traffic reset attempted (see $OUT/cleanup.log)"
}
trap cleanup EXIT INT TERM

route20() {
  VER=$(curl -s --max-time 10 "http://$ADMIN/admin/state" \
    -H "Authorization: Bearer ${LAB_ADMIN_TOKEN:-}" | python3 -c 'import sys,json;print(json.load(sys.stdin)["routingVersion"])')
  curl -s --max-time 10 -X PUT "http://$ADMIN/admin/routing" \
    -H "Authorization: Bearer ${LAB_ADMIN_TOKEN:-}" \
    -H 'Content-Type: application/json' \
    -d "{\"version\": $VER, \"candidatePercent\": 20}"
}

run_case() { # run_case <name> <lab_mode>
  echo "=== case $1 (mode=$2) ==="
  kubectl -n sre-lab patch deploy/api-candidate \
    -p "{\"spec\":{\"template\":{\"spec\":{\"containers\":[{\"name\":\"labapi\",\"args\":[\"-addr=:8081\",\"-mode=$2\"]}]}}}}" 2>/dev/null || true
  sleep 20 # warmup
  route20
  sleep 300 # observation
  END=$(date -u +%Y-%m-%dT%H:%M:%SZ)
  START=$(date -u -v-5M +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || date -u -d '5 min ago' +%Y-%m-%dT%H:%M:%SZ)
  go run ./cmd/budgetguard evaluate --config "$CFG" --prometheus "$PROM" \
    --start "$START" --end "$END" --out "$OUT/$1.json" || true
}

go build ./... # fail fast before touching the lab
run_case healthy healthy
run_case error error
run_case slow slow
python3 scripts/report.py "$OUT"
echo "demo complete → $OUT (routing reset by trap)"
