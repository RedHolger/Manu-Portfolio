#!/bin/bash
# disk-watchdog.sh — during-run floor enforcement. Between-run checks cannot
# stop a run from exhausting disk, so this loop polls free space and KILLS
# the load generator + plants a STOP file the suite honors after the load.
# Usage: disk-watchdog.sh <stopfile> [min_gb=2] [interval_s=10]
# min_gb / interval also honored from MIN_GB / WATCH_STEP env (env wins only
# when the positional is absent — a silent default floor caused this watchdog
# to loop forever in tests that passed MIN_GB by environment).
set -u
STOP="$1"
MIN_GB="${MIN_GB:-${2:-2}}"
python3 -c 'import math,sys; n=float(sys.argv[1]); sys.exit(0 if math.isfinite(n) and n>=2 else 1)' "$MIN_GB" || { echo "floor must be at least 2GiB" >&2; exit 1; }
STEP="${WATCH_STEP:-${3:-10}}"
while true; do
  sleep "$STEP"
  free_gb=$(python3 -c 'import shutil; print(min(shutil.disk_usage("/").free, shutil.disk_usage(".").free) / (1024**3))')
  low=$(python3 -c "print('yes' if float('$free_gb') < float('$MIN_GB') else 'no')")
  if [ "$low" = "yes" ]; then
    echo "WATCHDOG: ${free_gb}GiB < ${MIN_GB}GiB floor — killing load, planting STOP" >&2
    # Match every launcher form: labload-bin, `go run` temp binaries
    # (exe/labload), and direct cmd invocations (H3: pattern must not
    # assume one binary name).
    pkill -f 'labload-bin run' 2>/dev/null
    pkill -f 'exe/labload' 2>/dev/null
    pkill -f 'cmd/labload' 2>/dev/null
    touch "$STOP"
    exit 1
  fi
done
