#!/bin/bash
# validate-configs.sh — lint gate for YAML contracts (unknown-field guard is
# enforced by the Go parser in B1; this checks presence + basic shape).
set -euo pipefail
fail=0
for f in configs/slos/*.yaml configs/releases/*.yaml; do
  [ -e "$f" ] || { echo "MISS: no $f"; fail=1; continue; }
  python3 - "$f" <<'EOF'
import sys, yaml
p = sys.argv[1]
d = yaml.safe_load(open(p))
assert d.get("apiVersion") == "portfolio.sre/v1", p
assert d.get("kind") in ("ServiceSLO", "ReleaseProfile", "FaultExperiment", "RecoveryPolicy"), p
print("OK", p)
EOF
done
exit $fail
