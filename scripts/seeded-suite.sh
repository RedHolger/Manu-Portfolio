#!/bin/bash
# seeded-suite.sh — native seeded release suite (B4 subset, no kind).
# 5 seeds x {healthy,error,slow} = 15 live reps. State reset per rep:
# labapis restarted (fresh MemStore stock), gateway restarted (fresh
# counters + routing re-applied). One fresh Prometheus for the suite.
# Every outcome preserved; confusion matrix keeps INCONCLUSIVE separate.
# Usage: ./scripts/seeded-suite.sh   (needs /tmp/{labapi,labgateway,labload,budgetguard} built)
set -u
ADMIN=127.0.0.1:8082
PROM=http://127.0.0.1:9090
CFG=configs/slos/reservations.yaml
TS="$(date -u +%Y%m%dT%H%M%SZ)"
OUT="results/budgetguard/seeded-$TS"
mkdir -p "$OUT"
export LAB_ADMIN_TOKEN="${LAB_ADMIN_TOKEN:?set to the lab admin token (never commit it)}"

restart_stack() { # restart_stack <candidate-mode>
  pkill -f '/tmp/labapi'; pkill -f '/tmp/labgateway'; sleep 1
  nohup /tmp/labapi -addr=:8081 -mode=healthy > /tmp/labapi-stable.log 2>&1 &
  nohup /tmp/labapi -addr=:8083 -mode="$1" > /tmp/labapi-cand.log 2>&1 &
  sleep 1
  nohup /tmp/labgateway -addr=:8080 -admin-addr="$ADMIN" \
    -stable=http://127.0.0.1:8081 -candidate=http://127.0.0.1:8083 > /tmp/gateway.log 2>&1 &
  sleep 2
  VER=$(curl -s --max-time 10 "http://$ADMIN/admin/state" \
    -H "Authorization: Bearer $LAB_ADMIN_TOKEN" | python3 -c 'import sys,json;print(json.load(sys.stdin)["routingVersion"])')
  curl -s --max-time 10 -X PUT "http://$ADMIN/admin/routing" \
    -H "Authorization: Bearer $LAB_ADMIN_TOKEN" -H 'Content-Type: application/json' \
    -d "{\"version\": $VER, \"candidatePercent\": 20}"
  echo
}

run_rep() { # run_rep <class> <mode> <seed>
  echo "=== rep $1 seed $3 ==="
  restart_stack "$2"
  /tmp/labload run --gateway http://127.0.0.1:8080 --rate 70 --duration 80s \
    --seed "$3" --output "$OUT/$1-$3.jsonl" --summary "$OUT/$1-$3.summary.json"
  END=$(date -u +%Y-%m-%dT%H:%M:%SZ)
/tmp/budgetguard evaluate --config "$CFG" --prometheus "$PROM" \
    --start "$END" --end "$END" --out "$OUT/$1-$3.decision.json" || true
}

pkill -f 'storage.tsdb.path=/tmp/prom-suite'; sleep 1
rm -rf /tmp/prom-suite; mkdir -p /tmp/prom-suite
nohup /opt/homebrew/bin/prometheus \
  --config.file="$PWD/monitoring/prometheus.yaml" \
  --storage.tsdb.path=/tmp/prom-suite \
  --web.listen-address=127.0.0.1:9090 > /tmp/prom-suite.log 2>&1 &
sleep 4
curl -s "http://127.0.0.1:9090/-/healthy" || { echo "prometheus failed to start"; exit 1; }
echo "prometheus ready"

for seed in 11 22 33 44 55; do run_rep healthy healthy "$seed"; done
for seed in 11 22 33 44 55; do run_rep error error "$seed"; done
for seed in 11 22 33 44 55; do run_rep slow slow "$seed"; done

python3 - "$OUT" <<'EOF'
import json, sys
from pathlib import Path
d = Path(sys.argv[1])
matrix = {}
files = sorted(d.glob("*.decision.json"))
for p in files:
    cls = p.name.split("-")[0]
    r = json.loads(p.read_text())
    matrix.setdefault(cls, {}).setdefault(r["decision"], []).append(p.name)
lines = ["# Seeded native suite — confusion matrix", "",
         f"runs: {len(files)} (5 seeds x healthy/error/slow), INCONCLUSIVE kept separate", "",
         "| actual \\ predicted | PASS | FAIL | INCONCLUSIVE |",
         "|---|---|---|---|"]
for cls in ["healthy", "error", "slow"]:
    row = matrix.get(cls, {})
    fmt = lambda k: ", ".join(sorted(row.get(k, []))) or "—"
    lines.append(f"| {cls} | {fmt('PASS')} | {fmt('FAIL')} | {fmt('INCONCLUSIVE')} |")
(d / "matrix.md").write_text("\n".join(lines) + "\n")
print("\n".join(lines))
EOF
echo "suite complete → $OUT"
