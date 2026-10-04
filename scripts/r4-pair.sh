#!/bin/bash
# Compatibility entrypoint; the Python driver owns all subprocesses and evidence.
set -euo pipefail
exec python3 scripts/recoverops-experiment.py pair --out "$1" --seed "$2" --order "$3"
