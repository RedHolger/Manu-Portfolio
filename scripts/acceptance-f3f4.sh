#!/bin/bash
# acceptance-f3f4.sh — ONE bounded F3/F4 acceptance pass (no benchmarks).
# Covers: UID-precondition pod deletion + replacement readiness;
# dependency-fault activation, TTL expiry, traffic recovery;
# oracle over real request history + PostgreSQL.
# Guards: dedicated context, disk floor + watchdog. Stops (preserving
# evidence) on any guard trip. No deletions of user data.
set -u
export PATH="/opt/homebrew/bin:/opt/homebrew/opt/postgresql@16/bin:$PATH"
TS="$(date -u +%Y%m%dT%H%M%SZ)"
OUT="results/faultlab/acceptance-$TS"
mkdir -p "$OUT"
export LAB_ADMIN_TOKEN="$(cat .admin-token.env)"
TOKEN="$LAB_ADMIN_TOKEN"
GWBIN=./scripts/accept-faultlab-bin

./scripts/require-context.sh || exit 1
MIN_GB=1 ./scripts/disk-floor.sh || exit 1
./scripts/disk-watchdog.sh "$OUT/STOP" 1 10 > "$OUT/watchdog.log" 2>&1 &
echo $! > "$OUT/watchdog.pid"
stop_fwds() { pkill -f 'port-forward deploy/' 2>/dev/null; pkill -f 'port-forward svc/' 2>/dev/null; sleep 1; }
trap stop_fwds EXIT

log() { echo "[accept] $*" | tee -a "$OUT/steps.log"; }
fail() { echo "[accept] FAIL: $*" | tee -a "$OUT/failures.log"; }

go build -o "$GWBIN" ./cmd/faultlab || { fail "build"; exit 1; }

# 0. Refresh images (dep-fault endpoint) + manifests, scale up traffic path.
./scripts/build-images.sh > "$OUT/build.log" 2>&1 || { fail "build-images"; exit 1; }
kubectl apply -k deploy/base > "$OUT/apply.log" 2>&1 || { fail "apply"; exit 1; }
kubectl -n sre-lab create configmap prometheus-rules \
  --from-file=generated-rules.yaml=monitoring/generated-rules.yaml \
  --dry-run=client -o yaml | kubectl apply -f - >> "$OUT/apply.log" 2>&1
kubectl -n sre-lab rollout restart deploy/api-stable deploy/api-candidate deploy/labgateway > /dev/null
kubectl -n sre-lab rollout status deploy/api-stable --timeout=180s >> "$OUT/apply.log" 2>&1 || { fail "api-stable"; exit 1; }
kubectl -n sre-lab rollout status deploy/api-candidate --timeout=180s >> "$OUT/apply.log" 2>&1 || { fail "api-candidate"; exit 1; }
kubectl -n sre-lab rollout status deploy/labgateway --timeout=180s >> "$OUT/apply.log" 2>&1 || { fail "gateway"; exit 1; }
kubectl -n sre-lab port-forward deploy/labgateway 8082:8082 > /tmp/pf-acc-adm.log 2>&1 &
sleep 3
VER=$(curl -s --max-time 10 "http://127.0.0.1:8082/admin/state" -H "Authorization: Bearer $TOKEN" | python3 -c 'import sys,json;print(json.load(sys.stdin)["routingVersion"])')
curl -s --max-time 10 -X PUT "http://127.0.0.1:8082/admin/routing" -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' -d "{\"version\": $VER, \"candidatePercent\": 0}" > /dev/null
log "lab up, routing stable-only"

# 1. UID-precondition pod deletion + replacement readiness.
[ -f "$OUT/STOP" ] && { fail "stopped before pod-delete"; exit 1; }
"$GWBIN" pod-delete --deployment api-stable > "$OUT/pod-delete.json" 2>&1 || { fail "pod-delete"; exit 1; }
cat "$OUT/pod-delete.json" | tee -a "$OUT/steps.log" | tail -2
kubectl -n sre-lab rollout status deploy/api-stable --timeout=180s >> "$OUT/apply.log" 2>&1 || { fail "replacement ready"; exit 1; }
log "pod deleted by UID, replacement ready"

# 2. Dependency fault: arm on ALL stable pods (flag is per-pod memory),
# observe 503s under small load, TTL expiry, recover.
for POD in $(kubectl -n sre-lab get pods -l app=labapi,slot=stable -o jsonpath='{.items[*].metadata.name}'); do
  kubectl -n sre-lab port-forward "pod/$POD" 8084:8084 > /tmp/pf-acc-api.log 2>&1 &
  sleep 2
  "$GWBIN" dep-fault --api-admin http://127.0.0.1:8084 --fail --ttl 45 >> "$OUT/steps.log" 2>&1 || { fail "dep arm $POD"; exit 1; }
  pkill -f 'port-forward pod/' 2>/dev/null
done
go run ./cmd/labload run --gateway http://127.0.0.1:8080 --rate 10 --duration 30s --seed 501 --output "$OUT/depfault-load.jsonl" --summary "$OUT/depfault-load.summary.json" >> "$OUT/steps.log" 2>&1
python3 -c "
import json
from collections import Counter
lines = [json.loads(l) for l in open('$OUT/depfault-load.jsonl')]
print('during-fault codes:', dict(Counter(l['code'] for l in lines)))
" | tee -a "$OUT/steps.log"
sleep 25 # past TTL: lazy expiry on next request
go run ./cmd/labload run --gateway http://127.0.0.1:8080 --rate 10 --duration 20s --seed 502 --output "$OUT/recover-load.jsonl" --summary "$OUT/recover-load.summary.json" >> "$OUT/steps.log" 2>&1
python3 -c "
import json
from collections import Counter
lines = [json.loads(l) for l in open('$OUT/recover-load.jsonl')]
print('after-expiry codes:', dict(Counter(l['code'] for l in lines)))
" | tee -a "$OUT/steps.log"
# Explicit disarm (TTL already expired it; proves idempotent clear path).
LASTPOD=$(kubectl -n sre-lab get pods -l app=labapi,slot=stable -o jsonpath='{.items[0].metadata.name}')
kubectl -n sre-lab port-forward "pod/$LASTPOD" 8084:8084 > /tmp/pf-acc-api2.log 2>&1 &
sleep 2
"$GWBIN" dep-fault --api-admin http://127.0.0.1:8084 --clear >> "$OUT/steps.log" 2>&1 || { fail "dep clear"; exit 1; }
pkill -f 'port-forward pod/' 2>/dev/null

# 3. Oracle over real history: 10 known-key reservations via gateway.
kubectl -n sre-lab port-forward svc/postgres 5433:5432 > /tmp/pf-acc-pg.log 2>&1 &
sleep 3
PGPASS="$(kubectl -n sre-lab get secret postgres-pass -o jsonpath='{.data.password}' | base64 -d)"
python3 - "$OUT" <<'EOF' || exit 1
import json, subprocess, sys
out = sys.argv[1]
ops = []
for i in range(10):
    key = f"acc-{i}"
    r = subprocess.run(["curl", "-s", "--max-time", "10", "-X", "POST",
                        "http://127.0.0.1:8080/v1/reservations",
                        "-H", "Content-Type: application/json",
                        "-H", f"Idempotency-Key: {key}",
                        "-H", f"X-Operation-ID: {key}",
                        "-d", '{"sku":"demo-item","quantity":1}'],
                       capture_output=True, text=True)
    try:
        rid = json.loads(r.stdout)["id"]
        ops.append({"op_id": f"op-{i}", "key": key, "sku": "demo-item",
                    "qty": 1, "acked": True, "reservation_id": rid})
    except Exception as e:
        print("curl failed:", r.stdout, r.stderr)
        sys.exit(1)
# replay key 0 (same payload): must resolve to the same reservation
r = subprocess.run(["curl", "-s", "--max-time", "10", "-X", "POST",
                    "http://127.0.0.1:8080/v1/reservations",
                    "-H", "Content-Type: application/json",
                    "-H", "Idempotency-Key: acc-0",
                    "-H", "X-Operation-ID: acc-0r",
                    "-d", '{"sku":"demo-item","quantity":1}'],
                   capture_output=True, text=True)
rid = json.loads(r.stdout)["id"]
ops.append({"op_id": "op-0r", "key": "acc-0", "sku": "demo-item",
            "qty": 1, "acked": True, "reservation_id": rid})
json.dump(ops, open(out + "/oracle-ops.json", "w"), indent=1)
print("ops recorded:", len(ops), "replay same id:", rid == ops[0]["reservation_id"])
EOF
export PGPASSWORD="$PGPASS"
"$GWBIN" oracle-check --ops "$OUT/oracle-ops.json" \
  --pg-dsn "postgres://lab@127.0.0.1:5433/lab?sslmode=disable" \
  --sku demo-item --out "$OUT/oracle" 2>&1 | tee -a "$OUT/steps.log" || { fail "oracle"; exit 1; }
unset PGPASSWORD

kill "$(cat "$OUT/watchdog.pid")" 2>/dev/null
stop_fwds
df -h / | tail -1 > "$OUT/disk-end.txt"
echo "ACCEPTANCE COMPLETE → $OUT" | tee -a "$OUT/steps.log"
