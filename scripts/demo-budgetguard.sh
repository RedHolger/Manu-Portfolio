#!/bin/bash
# Safe full-window standalone demo; shared implementation preserves candidate args.
set -euo pipefail
exec python3 scripts/demo-portfolio.py --product budgetguard --out "results/budgetguard/demo-$(date -u +%Y%m%dT%H%M%SZ)"
