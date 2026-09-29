#!/bin/bash
# disk-floor.sh — refuse to start/continue experiment runs below MIN_GB free.
# A ~2GB cleanup is NOT automatically enough for a full suite: observed
# growth was ~35MB/rep host-side plus Docker VM image growth (2.0->2.7GB
# across ~9 in-kind reps on 2026-09-27). Start suites with >=2GB free AND
# keep this floor armed between reps; the suite scripts call this before
# every rep and abort (preserving outcomes) on violation.
# Usage: ./scripts/disk-floor.sh  (default floor 2GiB; MIN_GB env overrides)
set -u
MIN_GB="${MIN_GB:-2}"
free_gb=$(python3 -c 'import shutil; print(round(shutil.disk_usage("/").free / 1e9, 2))')
ok=$(python3 -c "print('yes' if float('$free_gb') >= float('$MIN_GB') else 'no')")
if [ "$ok" != "yes" ]; then
  echo "BELOW FLOOR: ${free_gb}GiB free < ${MIN_GB}GiB - refusing run (outcomes preserved)" >&2
  exit 1
fi
echo "disk OK: ${free_gb}GiB free >= ${MIN_GB}GiB floor"
