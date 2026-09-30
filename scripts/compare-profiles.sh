#!/bin/bash
# compare-profiles.sh — FaultLab baseline-versus-resilient paired comparison.
# Profiles differ ONLY in labload --correctness-profile (retry ambiguous
# once, same key); see configs/profiles/*.yaml and D-011. Everything else
# (rate, duration, seed, timeout, fault params, initial DB) is identical
# within a pair. Faults: gateway_delay (probabilistic) and pod_delete
# (real failover). Single-pod dep-fault excluded (pinning luck, D-011).
# Usage: ./scripts/compare-profiles.sh pilot   # 1 pair, delay, seed 910
#        ./scripts/compare-profiles.sh matrix  # 2 faults x 5 seeds, alternating
# Guards: dedicated context, disk floor + watchdog, per-step STOP gates.
# Failed/invalid runs are preserved and reported, never deleted or rerun
# silently. No benchmark repetition beyond the matrix.
set -u
export PATH="/opt/homebrew/bin:/opt/homebrew/opt/postgresql@16/bin:$PATH"
TS="$(date -u +%Y%m%dT%H%M%SZ)"
OUT="results/faultlab/compare-$TS"
mkdir -p "$OUT"
echo "$OUT" > /tmp/cmp-out.txt
export LAB_ADMIN_TOKEN="$(cat .admin-token.env)"
TOKEN="$LAB_ADMIN_TOKEN"
GW_ADMIN="http://127.0.0.1:8082"
GW_PUB="http://127.0.0.1:8080"

./scripts/require-context.sh || exit 1
./scripts/disk-floor.sh || exit 1
./scripts/disk-watchdog.sh "$OUT/STOP" 2 10 > "$OUT/watchdog.log" 2>&1 &
echo $! > "$OUT/watchdog.pid"

LABLOAD_BIN=/tmp/cmp-labload-bin
FAULTLAB_BIN=/tmp/cmp-faultlab-bin
go build -o "$LABLOAD_BIN" ./cmd/labload || exit 1
go build -o "$FAULTLAB_BIN" ./cmd/faultlab || exit 1

# Session port-forwards (one pair for the whole matrix).
kubectl --context kind-sre-lab -n sre-lab port-forward deploy/labgateway 8082:8082 > /tmp/pf-cmp-adm.log 2>&1 &
kubectl --context kind-sre-lab -n sre-lab port-forward svc/postgres 5433:5432 > /tmp/pf-cmp-pg.log 2>&1 &
sleep 4
stop_all() {
  pkill -f 'port-forward deploy/labgateway' 2>/dev/null
  pkill -f 'port-forward svc/postgres' 2>/dev/null
  kill "$(cat "$OUT/watchdog.pid")" 2>/dev/null
  true
}
trap stop_all EXIT

log() { echo "[compare] $*" | tee -a "$OUT/steps.log"; }
fail() { echo "[compare] FAIL: $*" | tee -a "$OUT/failures.log"; }
gate() {
  if [ -f "$OUT/STOP" ]; then fail "STOP present before $1 — halting"; exit 1; fi
  ./scripts/disk-floor.sh || { fail "floor before $1"; exit 1; }
}

# Shared workload — the ONLY profile difference is --correctness-profile.
RATE=10; DUR=100s; TIMEOUT=2s
# Fault schedule inside the 100s load: arm t+20, clear t+60 (40s fault).
ARM_AT=20; CLEAR_AT=60

# Sanity: routing must be stable-only; faults must be empty before starting.
CUR=$(curl -s --max-time 10 "$GW_ADMIN/admin/state" -H "Authorization: Bearer $TOKEN")
echo "$CUR" | grep -q '"candidatePercent":0' || { fail "routing not stable-only: $CUR"; exit 1; }
echo "$CUR" | grep -q '"faults":null' || { fail "live faults present: $CUR"; exit 1; }
log "preconditions ok: stable-only routing, no live faults"

reseed() {
  PGPASS="$(kubectl --context kind-sre-lab -n sre-lab get secret postgres-pass -o jsonpath='{.data.password}' | base64 -d)"
  PGPASSWORD="$PGPASS" psql -h 127.0.0.1 -p 5433 -U lab -d lab -q -c \
    "TRUNCATE reservations; INSERT INTO inventory(sku,initial_stock,available) VALUES('demo-item',100000,100000) ON CONFLICT(sku) DO UPDATE SET initial_stock=100000, available=100000;" || return 1
  unset PGPASSWORD
}

arm_delay() { # <fault-id>
  curl -s --max-time 10 -X PUT "$GW_ADMIN/admin/faults/$1" -H "Authorization: Bearer $TOKEN" \
    -H 'Content-Type: application/json' \
    -d '{"kind":"gateway_delay","slot":"stable","delayMilliseconds":3000,"fraction":0.5,"ttlSeconds":55}' || return 1
}
clear_delay() { # <fault-id>
  curl -s --max-time 10 -X DELETE "$GW_ADMIN/admin/faults/$1" -H "Authorization: Bearer $TOKEN" > /dev/null || return 1
}

# run_one FAULT SEED PROFILE — one labload run with timed fault + oracle.
run_one() {
  local fault="$1" seed="$2" profile="$3"
  local name="run-${fault}-${seed}-${profile}"
  local dir="$OUT/$name"
  mkdir -p "$dir"
  gate "$name"
  log "=== $name ==="
  reseed || { fail "$name reseed"; echo '{"status":"reseed-failed"}' > "$dir/meta.json"; return 0; }

  local cmd=("$LABLOAD_BIN" run --gateway "$GW_PUB" --rate "$RATE" --duration "$DUR"
    --seed "$seed" --timeout "$TIMEOUT" --invalid-fraction 0
    --output "$dir/load.jsonl" --summary "$dir/load.summary.json")
  if [ "$profile" = "resilient" ]; then cmd+=(--correctness-profile); fi
  printf '%s ' "${cmd[@]}" > "$dir/command.txt"; echo >> "$dir/command.txt"

  local t0; t0=$(date +%s)
  "${cmd[@]}" > "$dir/console.log" 2>&1 &
  local loadpid=$!
  sleep "$ARM_AT"
  local t_arm; t_arm=$(date +%s)
  if [ "$fault" = "delay" ]; then
    arm_delay "cmp-$name" >> "$dir/fault.log" 2>&1 || fail "$name arm"
  else
    "$FAULTLAB_BIN" pod-delete --deployment api-stable >> "$dir/fault.log" 2>&1 &
    echo $! > "$dir/poddelete.pid"
  fi
  sleep $((CLEAR_AT - ARM_AT))
  local t_clear; t_clear=$(date +%s)
  if [ "$fault" = "delay" ]; then
    clear_delay "cmp-$name" >> "$dir/fault.log" 2>&1 || fail "$name clear"
  fi
  wait "$loadpid"; local lexit=$?
  if [ "$fault" = "pod" ]; then
    wait "$(cat "$dir/poddelete.pid")"; echo "poddelete exit=$?" >> "$dir/fault.log"
    kubectl --context kind-sre-lab -n sre-lab rollout status deploy/api-stable \
      --timeout=180s >> "$dir/fault.log" 2>&1 || fail "$name replacement"
  fi
  local t_end; t_end=$(date +%s)
  # Cleanup verification: no gateway faults may remain live.
  local st; st=$(curl -s --max-time 10 "$GW_ADMIN/admin/state" -H "Authorization: Bearer $TOKEN")
  echo "$st" | grep -q '"faults":null' || fail "$name live faults remain: $st"

  python3 - "$dir" "$fault" "$seed" "$profile" "$lexit" "$t0" "$t_arm" "$t_clear" "$t_end" <<'EOF'
import json, sys
d, fault, seed, profile, lexit, t0, ta, tc, te = sys.argv[1], sys.argv[2], int(sys.argv[3]), sys.argv[4], int(sys.argv[5]), int(sys.argv[6]), int(sys.argv[7]), int(sys.argv[8]), int(sys.argv[9])
s = json.load(open(d + "/load.summary.json"))
json.dump({"run": d.split("/")[-1], "fault": fault, "seed": seed, "profile": profile,
           "labload_exit": lexit, "load_valid": s.get("Valid", False),
           "t_start": t0, "t_arm": ta, "t_clear": tc, "t_end": te,
           "summary": s}, open(d + "/meta.json", "w"), indent=1)
print("load exit:", lexit, "valid:", s.get("Valid"), "offered:", s.get("Offered"))
EOF
  # Oracle over this run's keys against the per-run-fresh DB.
  gate "$name-oracle"
  python3 - "$dir" "$seed" <<'EOF' || fail "$name ops build"
import json, sys
d, seed = sys.argv[1], int(sys.argv[2])
def fnv1a64(s):
    h = 14695981039346656037
    for b in s.encode():
        h ^= b
        h = (h * 1099511628211) % 2**64
    return "%016x" % h
n_max = json.load(open(d + "/load.summary.json"))["Offered"]
key_of = {}
for n in range(1, n_max + 1):
    key_of[fnv1a64(f"load-{seed}-{n}")] = f"load-{seed}-{n}"
ops = []
for line in open(d + "/load.jsonl"):
    a = json.loads(line)
    key = key_of.get(a["key_hash"], "UNKNOWN-" + a["key_hash"][:8])
    if 200 <= a["code"] < 300:
        ops.append({"op_id": a["op_id"], "key": key, "sku": "demo-item",
                    "qty": 1, "acked": True, "reservation_id": a.get("reservation_id", "")})
    else:
        ops.append({"op_id": a["op_id"], "key": key, "sku": "demo-item",
                    "qty": 1, "acked": False})
json.dump(ops, open(d + "/oracle-ops.json", "w"), indent=1)
print("ops:", len(ops), "acked:", sum(1 for o in ops if o["acked"]))
EOF
  PGPASS="$(kubectl --context kind-sre-lab -n sre-lab get secret postgres-pass -o jsonpath='{.data.password}' | base64 -d)"
  "$FAULTLAB_BIN" oracle-check --ops "$dir/oracle-ops.json" \
    --pg-dsn "postgres://lab@127.0.0.1:5433/lab?sslmode=disable" \
    --sku demo-item --out "$dir/oracle" >> "$dir/console.log" 2>&1 || fail "$name oracle violations"
  unset PGPASS
  log "$name done"
}

# verify_pair SEED FAULT — the two commands must differ ONLY by profile flag
# (and their own output paths).
verify_pair() {
  python3 - "$OUT/run-$2-$1-baseline/command.txt" "$OUT/run-$2-$1-resilient/command.txt" <<'EOF' || exit 1
import sys
b = open(sys.argv[1]).read().split()
r = open(sys.argv[2]).read().split()
strip = lambda toks: [t for i, t in enumerate(toks)
                      if t != "--correctness-profile"
                      and not t.endswith("/load.jsonl") and not t.endswith("/load.summary.json")
                      and not (i > 0 and (toks[i-1] in ("--output", "--summary")))]
if strip(b) != strip(r):
    print("PROFILE COMMANDS DIFFER BEYOND FLAG:\nBASE:", b, "\nRES :", r)
    sys.exit(1)
if "--correctness-profile" not in r or "--correctness-profile" in b:
    print("profile flag misapplied")
    sys.exit(1)
print("pair commands differ only by --correctness-profile: OK")
EOF
}

run_pair() { # FAULT SEED FIRST(second auto)
  local fault="$1" seed="$2" first="$3" second=resilient
  [ "$first" = "resilient" ] && second=baseline
  run_one "$fault" "$seed" "$first"
  run_one "$fault" "$seed" "$second"
  verify_pair "$seed" "$fault" | tee -a "$OUT/steps.log" || { fail "pair $fault/$seed command drift"; exit 1; }
}

MODE="${1:-pilot}"
if [ "$MODE" = "pilot" ]; then
  log "PILOT: one delay pair, seed 905, baseline first"
  run_pair delay 905 baseline
elif [ "$MODE" = "matrix" ]; then
  i=0
  for fault in delay pod; do
    for seed in 910 920 930 940 950; do
      if [ $((i % 2)) -eq 0 ]; then run_pair "$fault" "$seed" baseline;
      else run_pair "$fault" "$seed" resilient; fi
      i=$((i + 1))
    done
  done
else
  echo "usage: $0 pilot|matrix" >&2; exit 1
fi

df -h /System/Volumes/Data | tail -1 > "$OUT/disk-end.txt"
echo "COMPARE $MODE COMPLETE → $OUT" | tee -a "$OUT/steps.log"
