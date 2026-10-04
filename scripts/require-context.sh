#!/bin/bash
# require-context.sh — refuse mutating commands outside the dedicated cluster.
# A namespace named sre-lab alone does not prove which cluster kubectl talks
# to (H4). The dedicated context cannot be overridden by environment.
set -u
want="kind-sre-lab"
got="$(kubectl config current-context 2>/dev/null)" || { echo "refusing: no current kube context" >&2; exit 1; }
[ "$got" = "$want" ] || { echo "refusing: context '$got' != dedicated '$want'" >&2; exit 1; }
echo "context OK: $got"
