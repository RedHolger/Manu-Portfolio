#!/bin/bash
# disk-watchdog.sh — during-run floor enforcement. Between-run checks cannot
# stop a run from exhausting disk, so this loop polls free space and KILLS
# the load generator + plants a STOP file the suite honors after the load.
# Usage: disk-watchdog.sh <stopfile> [min_gb=2] [interval_s=10]
# Stop it after the suite (it exits on STOP or when killed).
set -u
STOP="$1"
MIN_GB="${2:-2}"
STEP="${3:-10}"
while true; do
  sleep "$STEP"
  free_gb=$(python3 -c 'import shutil; print(round(shutil.disk_usage("/").free / 1e9, 2))')
  low=$(python3 -c "print('yes' if float('$free_gb') < float('$MIN_GB') else 'no')")
  if [ "$low" = "yes" ]; then
    echo "WATCHDOG: ${free_gb}GiB < ${MIN_GB}GiB floor — killing load, planting STOP" >&2
    pkill -f 'labload-bin run' 2>/dev/null
    touch "$STOP"
    exit 1
  fi
done
