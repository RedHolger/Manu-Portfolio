#!/bin/bash
# r4-pair.sh — one measured R4 pair (controller arm + simulated-manual arm).
# Usage: r4-pair.sh <pairdir> <seed-base> <order>
#   order = controller-first | baseline-first (alternating per plan).
# Each arm: wait rule clear -> degrade (error 0.5) -> traffic -> AM fires ->
# rollback -> 3x10s measured windows -> RESOLVED. Baseline arm holds the
# identical rollback 120s after alert receipt (simulated fixed-delay
# response; NOT human on-call evidence). Cooldown (600s) respected: arms
# wait until 630s after the last recorded execution. Preserves all outcomes
# incl. failures. Port-forward re-established per poll (pod restarts kill it).
set -u
PAIRDIR="$1"; SEED="$2"; ORDER="$3"
NS=sre-lab; CTX=kind-sre-lab

pf() { # ensure port-forward alive
  curl -s -m 5 http://127.0.0.1:18089/readyz >/dev/null 2>&1 && return 0
  pkill -f "port-forward -n sre-lab.*18089" 2>/dev/null
  sleep 2
  (timeout 300 kubectl port-forward -n $NS svc/recoverops 18089:8089 >/dev/null 2>&1 &)
  sleep 6
  curl -s -m 5 http://127.0.0.1:18089/readyz >/dev/null 2>&1
}

api() { # $1=path -> stdout (fails loudly)
  pf || { echo "PORTFORWARD_FAILED" >&2; return 1; }
  curl -s -m 10 "http://127.0.0.1:18089$1" -H "Authorization: Bearer $(cat /tmp/ro-token.txt)" || return 1
}

degrade() {
  ./scripts/require-context.sh >/dev/null || return 1
  kubectl --context $CTX -n $NS patch deployment api-stable --type strategic \
    -p '{"spec":{"template":{"spec":{"containers":[{"name":"labapi","args":["-addr=:8081","-mode=error","-error-rate=0.5","-admin-addr=127.0.0.1:8084"]}]}}}}' >/dev/null || return 1
  kubectl --context $CTX -n $NS rollout status deploy/api-stable --timeout=75s >/dev/null || return 1
}

wait_clear() { # rule inactive; timeout ~10m
  for _ in $(seq 1 40); do
    N=$(curl -s -m 10 'http://127.0.0.1:9090/api/v1/alerts' | python3 -c "import json,sys; d=json.load(sys.stdin); print(len([a for a in d['data']['alerts'] if a['labels'].get('alertname')=='LabBadTemplate' and a['state']=='firing']))" 2>/dev/null)
    if [ "$N" = "0" ]; then return 0; fi
    sleep 15
  done
  return 1
}

wait_alert() { # -> activeAt; timeout ~12m
  for _ in $(seq 1 48); do
    AT=$(curl -s -m 10 'http://127.0.0.1:9090/api/v1/alerts' | python3 -c "import json,sys; d=json.load(sys.stdin); als=[a.get('activeAt','') for a in d['data']['alerts'] if a['labels'].get('alertname')=='LabBadTemplate' and a['state']=='firing']; print(als[0] if als else '')" 2>/dev/null)
    if [ -n "$AT" ]; then echo "$AT"; return 0; fi
    sleep 15
  done
  return 1
}

wait_executed() { # $1=created-after-iso; timeout ~6m -> incident id with claim event
  for _ in $(seq 1 24); do
    ID=$(api "/v1/incidents?limit=20" | python3 -c "import json,sys; d=json.load(sys.stdin); m=[i['id'] for i in d.get('incidents',[]) if i['created_at']>'$1']; print(m[-1] if m else '')" 2>/dev/null)
    if [ -n "$ID" ]; then
      HAS=$(api "/v1/incidents/$ID" | python3 -c "import json,sys; d=json.load(sys.stdin); print(any(e['kind']=='claim' for e in d.get('events',[])))" 2>/dev/null)
      if [ "$HAS" = "True" ]; then echo "$ID"; return 0; fi
    fi
    sleep 15
  done
  return 1
}

wait_resolved() { # $1=incident id; timeout ~10m
  for _ in $(seq 1 40); do
    ST=$(api "/v1/incidents/$1" | python3 -c "import json,sys; print(json.load(sys.stdin).get('incident',{}).get('state',''))" 2>/dev/null)
    if [ "$ST" = "RESOLVED" ]; then echo "$ST"; return 0; fi
    sleep 15
  done
  return 1
}

wait_cooldown() { # budget-aware: <3 executions in trailing 3600s AND 630s since last
  LOG="$PAIRDIR/../exec.log"
  while :; do
    NOW=$(date +%s)
    N=$(awk -v now="$NOW" '$1 > now-3600' "$LOG" 2>/dev/null | wc -l | tr -d ' ')
    LAST=$(awk -v now="$NOW" '$1 > now-3600' "$LOG" 2>/dev/null | tail -n 1)
    [ -z "$LAST" ] && LAST=0
    WAIT=$((630 - (NOW - LAST)))
    if [ "$N" -lt 3 ] && [ "$WAIT" -le 0 ]; then return 0; fi
    [ "$WAIT" -lt 60 ] && WAIT=60
    echo "budget wait ${WAIT}s (used $N/3 in trailing hour)" >&2
    sleep "$WAIT"
  done
}

note_exec() { # append execution epoch (called with acted epoch)
  echo "$1" >> "$PAIRDIR/../exec.log"
}

measure_windows() { # $1=outprefix $2,3,4=seeds -> verify exit
  for s in $2 $3 $4; do
    go run ./cmd/labload run --rate 15 --duration 10s --seed "$s" --output "$1-window-$s.jsonl" >/dev/null 2>&1 || return 1
  done
  python3 -c "
import json
ws=[]
for s in ($2,$3,$4):
    rows=[json.loads(l) for l in open('$1-window-'+str(s)+'.jsonl')]
    el=len(rows); ok=sum(1 for r in rows if r['code'] in (200,201)); fast=sum(1 for r in rows if r['code'] in (200,201) and r['latency_ms']<300)
    ws.append({'eligible':el,'success':ok,'fast_ok':fast})
json.dump({'desired_present':True,'generation_hit':True,'ready_replicas':2,'want_replicas':2,'windows':ws}, open('$1-verify-input.json','w'))
" || return 1
  go run ./cmd/recoverops verify --file "$1-verify-input.json" > "$1-verify.json" 2>&1
}

run_arm() { # $1=armname $2=seed $3=dir
  ARM="$1"; S="$2"; D="$3"
  wait_cooldown || return 1
  wait_clear || { echo "{\"arm\":\"$ARM\",\"outcome\":\"rule-not-clear\"}"; return 1; }
  T0=$(date -u +%Y-%m-%dT%H:%M:%SZ)
  degrade || { echo "{\"arm\":\"$ARM\",\"outcome\":\"degrade-failed\"}"; return 1; }
  (go run ./cmd/labload run --rate 15 --duration 280s --seed "$S" --output "$D/degrade-$ARM.jsonl" > "$D/degrade-$ARM.stdout" 2>&1 &)
  AT=$(wait_alert) || { echo "{\"arm\":\"$ARM\",\"outcome\":\"no-alert\"}"; return 1; }
  if [ "$ARM" = "baseline" ]; then
    kubectl --context $CTX -n $NS scale deploy/recoverops --replicas=0 >/dev/null
    sleep 120
    kubectl --context $CTX -n $NS scale deploy/recoverops --replicas=1 >/dev/null
    kubectl --context $CTX -n $NS rollout status deploy/recoverops --timeout=90s >/dev/null || return 1
  fi
  IID=$(wait_executed "$T0") || { WHY=$(api "/v1/incidents?limit=5" | python3 -c "import json,sys; d=json.load(sys.stdin); print(';'.join(i['id'][:8]+':'+i['state'] for i in d.get('incidents',[][:5])))" 2>/dev/null); echo "{\"arm\":\"$ARM\",\"alert_at\":\"$AT\",\"outcome\":\"no-execution\",\"recent\":\"$WHY\"}"; return 1; }
  ACT=$(date -u +%Y-%m-%dT%H:%M:%SZ)
  note_exec "$(date +%s)"
  measure_windows "$D/$ARM" "$((S+1))" "$((S+2))" "$((S+3))" || { echo "{\"arm\":\"$ARM\",\"incident\":\"$IID\",\"outcome\":\"verify-failed\"}"; return 1; }
  # Persisted verification record (RESOLVED requires this, not the
  # resolved webhook alone): POST the measured windows to the endpoint.
  pf || { echo "{\"arm\":\"$ARM\",\"incident\":\"$IID\",\"outcome\":\"verify-record-unreachable\"}"; return 1; }
  VR=$(timeout 30 curl -s -m 25 -X POST "http://127.0.0.1:18089/v1/incidents/$IID/verify" -H "Authorization: Bearer $(cat /tmp/ro-token.txt)" -H 'Content-Type: application/json' --data "@$D/$ARM-verify-input.json" 2>/dev/null)
  echo "$VR" > "$D/$ARM-verify-record.json"
  echo "$VR" | grep -q '"recovered": *true' || { echo "{\"arm\":\"$ARM\",\"incident\":\"$IID\",\"outcome\":\"verify-record-rejected\"}"; return 1; }
  wait_resolved "$IID" || { echo "{\"arm\":\"$ARM\",\"incident\":\"$IID\",\"alert_at\":\"$AT\",\"acted_at\":\"$ACT\",\"outcome\":\"unresolved\"}"; return 1; }
  RES=$(date -u +%Y-%m-%dT%H:%M:%SZ)
  echo "{\"arm\":\"$ARM\",\"incident\":\"$IID\",\"alert_at\":\"$AT\",\"acted_at\":\"$ACT\",\"resolved_at\":\"$RES\",\"outcome\":\"resolved\"}"
}

mkdir -p "$PAIRDIR"
./scripts/disk-floor.sh > "$PAIRDIR/floor.txt" 2>&1 || { echo '{"pair":"aborted-disk-floor"}'; exit 1; }
if [ "$ORDER" = "controller-first" ]; then
  C=$(run_arm controller "$SEED" "$PAIRDIR"); C_RC=$?
  B=$(run_arm baseline "$((SEED+10))" "$PAIRDIR"); B_RC=$?
else
  B=$(run_arm baseline "$SEED" "$PAIRDIR"); B_RC=$?
  C=$(run_arm controller "$((SEED+10))" "$PAIRDIR"); C_RC=$?
fi
echo "{\"controller\":$C,\"baseline\":$B}" > "$PAIRDIR/pair.json"
cat "$PAIRDIR/pair.json"
[ "$C_RC" = "0" ] && [ "$B_RC" = "0" ]
