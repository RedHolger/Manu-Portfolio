#!/bin/bash
# selftest.sh — executable tests for suite guard scripts (H3/H4).
# Run: ./scripts/selftest.sh   (no cluster, no Docker needed)
set -u
fails=0
. ./scripts/lib.sh

# 1. halted() honors STOP presence.
d=$(mktemp -d)
halted "$d" && { echo "FAIL: halted without STOP"; fails=$((fails+1)); } || echo "ok halted-absent"
touch "$d/STOP"
halted "$d" || { echo "FAIL: not halted with STOP"; fails=$((fails+1)); }
echo "ok halted-present"
rm -rf "$d"

# 2. watchdog kills every launcher form and plants STOP.
d=$(mktemp -d)
bash -c 'exec -a "labload-bin run" sleep 60' &
p1=$!
bash -c 'exec -a "/tmp/go-build999/b001/exe/labload" sleep 60' &
p2=$!
MIN_GB=999999 ./scripts/disk-watchdog.sh "$d/STOP" >/dev/null 2>&1
[ -f "$d/STOP" ] || { echo "FAIL: no STOP planted"; fails=$((fails+1)); }
sleep 1
kill -0 "$p1" 2>/dev/null && { echo "FAIL: labload-bin form survived"; fails=$((fails+1)); kill "$p1"; }
kill -0 "$p2" 2>/dev/null && { echo "FAIL: exe/labload form survived"; fails=$((fails+1)); kill "$p2"; }
echo "ok watchdog-kill+stop"
rm -rf "$d"

# 3. require-context.sh with stub kubectl.
stub=$(mktemp -d)
cat > "$stub/kubectl" <<'EOF'
#!/bin/bash
echo "$STUB_CONTEXT"
EOF
chmod +x "$stub/kubectl"
STUB_CONTEXT="kind-sre-lab" PATH="$stub:$PATH" ./scripts/require-context.sh >/dev/null \
  || { echo "FAIL: dedicated context refused"; fails=$((fails+1)); }
echo "ok context-accept"
STUB_CONTEXT="prod-evil" PATH="$stub:$PATH" ./scripts/require-context.sh >/dev/null 2>&1 \
  && { echo "FAIL: foreign context accepted"; fails=$((fails+1)); }
echo "ok context-refuse"
STUB_CONTEXT="anything" SRE_CONTEXT="anything" PATH="$stub:$PATH" ./scripts/require-context.sh >/dev/null \
  || { echo "FAIL: SRE_CONTEXT override refused"; fails=$((fails+1)); }
echo "ok context-override"
rm -rf "$stub"

if [ "$fails" -ne 0 ]; then echo "SELFTEST FAILURES: $fails"; exit 1; fi
echo "SELFTEST ALL PASS"
