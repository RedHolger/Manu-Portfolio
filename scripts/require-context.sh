#!/bin/bash
# require-context.sh — refuse mutating commands outside the dedicated cluster.
# A namespace named sre-lab alone does not prove which cluster kubectl talks
# to (H4). Override only for tests: SRE_CONTEXT=... require-context.sh
set -u
want="${SRE_CONTEXT:-kind-sre-lab}"
got="$(kubectl config current-context 2>/dev/null)" || { echo "refusing: no current kube context" >&2; exit 1; }
[ "$got" = "$want" ] || { echo "refusing: context '$got' != dedicated '$want'" >&2; exit 1; }
echo "context OK: $got"
