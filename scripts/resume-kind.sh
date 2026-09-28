#!/bin/bash
# resume-kind.sh — outstanding kind reps after halt: error x2 (777,888),
# slow x5 (111-555), client-error verification (1). Same isolation as the
# main suite (PG reseed, gateway+prometheus restart, routing re-applied).
# Watchdog + floor armed; STOP aborts with outcomes preserved.
set -u
export PATH="/opt/homebrew/bin:/opt/homebrew/opt/postgresql@16/bin:$PATH"
PROM=http://127.0.0.1:9090
GW=http://127.0.0.1:8080
CFG=configs/slos/reservations.yaml
TS="$(date -u +%Y%m%dT%H%M%SZ)"
OUT="results/budgetguard/kind-resume-$TS"
mkdir -p "$OUT"
export LAB_ADMIN_TOKEN="$(cat .admin-token.env)"
PGPASS="$(kubectl -n sre-lab get secret postgres-pass -o jsonpath='{.data.password}' | base64 -d)"

stop_fwds() { pkill -f 'port-forward svc/postgres' 2>/dev/null; pkill -f 'port-forward deploy/labgateway' 2>/dev/null; sleep 1; }
trap stop_fwds EXIT

reset_pg() {
  stop_fwds
  kubectl -n sre-lab port-forward svc/postgres 5433:5432 > /tmp/pf-pg-r.log 2>&1 &
  sleep 3
  PGPASSWORD="$PGPASS" psql -h 127.0.0.1 -p 5433 -U lab -d lab -q -c \
    "TRUNCATE reservations; INSERT INTO inventory(sku,initial_stock,available) VALUES('demo-item',100000,100000) ON CONFLICT(sku) DO UPDATE SET initial_stock=100000, available=100000;" || return 1
  stop_fwds
}

fresh_prom() {
  kubectl -n sre-lab rollout restart deploy/prometheus > /dev/null
  kubectl -n sre-lab rollout status deploy/prometheus --timeout=120s > /dev/null
}

fresh_gateway() {
  kubectl -n sre-lab rollout restart deploy/labgateway > /dev/null
  kubectl -n sre-lab rollout status deploy/labgateway --timeout=120s > /dev/null
  kubectl -n sre-lab port-forward deploy/labgateway 8082:8082 > /tmp/pf-adm-r.log 2>&1 &
  sleep 3
  VER=$(curl -s --max-time 10 "http://127.0.0.1:8082/admin/state" \
    -H "Authorization: Bearer $LAB_ADMIN_TOKEN" | python3 -c 'import sys,json;print(json.load(sys.stdin)["routingVersion"])')
  curl -s --max-time 10 -X PUT "http://127.0.0.1:8082/admin/routing" \
    -H "Authorization: Bearer $LAB_ADMIN_TOKEN" -H 'Content-Type: application/json' \
    -d "{\"version\": $VER, \"candidatePercent\": 20}"
  echo
}

set_mode() {
  kubectl -n sre-lab patch deploy/api-candidate \
    -p "{\"spec\":{\"template\":{\"spec\":{\"containers\":[{\"name\":\"labapi\",\"args\":[\"-addr=:8081\",\"-mode=$1\"]}]}}}}" > /dev/null
  kubectl -n sre-lab rollout status deploy/api-candidate --timeout=180s > /dev/null
}

run_rep() { # run_rep <class> <seed> [extra flags]
  echo "=== rep $1 seed $2 ==="
  MIN_GB=2 ./scripts/disk-floor.sh || { echo "FLOOR STOP $1 $2" >> "$OUT/failures.log"; return 1; }
  reset_pg || { echo "PG RESET FAILED $1 $2" >> "$OUT/failures.log"; return 1; }
  fresh_prom
  fresh_gateway
  sleep 10
  go run ./cmd/labload run --gateway "$GW" --rate 70 --duration 80s \
    --seed "$2" --output "$OUT/$1-$2.jsonl" --summary "$OUT/$1-$2.summary.json" ${3:-}
  if [ -f "$OUT/STOP" ]; then echo "WATCHDOG STOP after $1 $2" | tee -a "$OUT/failures.log"; return 2; fi
  END=$(date -u +%Y-%m-%dT%H:%M:%SZ)
  go run ./cmd/budgetguard evaluate --config "$CFG" --prometheus "$PROM" \
    --start "$END" --end "$END" --out "$OUT/$1-$2.decision.json" || true
}

go build -o scripts/labload-bin ./cmd/labload 2>/dev/null
go build -o scripts/budgetguard-bin ./cmd/budgetguard 2>/dev/null
./scripts/disk-watchdog.sh "$OUT/STOP" 2 10 > "$OUT/watchdog.log" 2>&1 &
echo $! > "$OUT/watchdog.pid"

set_mode error
for seed in 777 888; do run_rep error "$seed" || break; done
set_mode slow
for seed in 111 222 333 444 555; do run_rep slow "$seed" || break; done
set_mode healthy
# Client-error verification: 10% invalid traffic must still PASS (excluded).
run_rep clienterror 4242 --invalid-fraction=0.1 || true
set_mode healthy

kill "$(cat "$OUT/watchdog.pid")" 2>/dev/null
stop_fwds
df -h / | tail -1 > "$OUT/disk-end.txt"

python3 - "$OUT" <<'EOF'
import json, sys
from pathlib import Path
d = Path(sys.argv[1])
matrix = {}
for p in sorted(d.glob("*.decision.json")):
    cls = p.name.split("-")[0]
    r = json.loads(p.read_text())
    matrix.setdefault(cls, {}).setdefault(r["decision"], []).append(
        f"{p.name} cand={r['candidate']['eligible']}/{r['candidate']['bad']}/{r['candidate']['slow_or_bad']}")
lines = ["# Kind resume suite — confusion matrix", "",
         "error x2 + slow x5 + clienterror x1 (kind-sre-lab), INCONCLUSIVE kept separate", "",
         "| actual \\ predicted | PASS | FAIL | INCONCLUSIVE |",
         "|---|---|---|---|"]
for cls in ["error", "slow", "clienterror"]:
    row = matrix.get(cls, {})
    fmt = lambda k: "; ".join(sorted(row.get(k, []))) or "—"
    lines.append(f"| {cls} | {fmt('PASS')} | {fmt('FAIL')} | {fmt('INCONCLUSIVE')} |")
(d / "matrix.md").write_text("\n".join(lines) + "\n")
print("\n".join(lines))
EOF
echo "resume complete → $OUT"
