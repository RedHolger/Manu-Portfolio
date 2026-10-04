#!/bin/bash
# Apply to the existing lab; no cluster deletion, pruning or DB reset.
set -euo pipefail
./scripts/require-context.sh
./scripts/disk-floor.sh
: "${RECOVEROPS_TOKEN:?set a nonempty token without trailing newline}"
: "${LAB_ADMIN_TOKEN:?required for the shared gateway secret}"
K=(kubectl --context kind-sre-lab -n sre-lab)
./scripts/build-images.sh
# Tokens go through stdin only; never command-line literals or tracked files.
python3 - <<'PY' | "${K[@]}" apply -f -
import os,json,base64
items=[]
for name,key,env in [('recoverops-token','token','RECOVEROPS_TOKEN'),('lab-admin','token','LAB_ADMIN_TOKEN')]:
 value=os.environ[env]
 if value!=value.strip():raise SystemExit('token has leading/trailing whitespace; fix environment')
 items.append({'apiVersion':'v1','kind':'Secret','metadata':{'name':name,'namespace':'sre-lab'},'data':{key:base64.b64encode(value.encode()).decode()}})
print(json.dumps({'apiVersion':'v1','kind':'List','items':items}))
PY
"${K[@]}" create configmap prometheus-rules --from-file=generated-rules.yaml=monitoring/generated-rules.yaml --dry-run=client -o yaml | "${K[@]}" apply -f -
"${K[@]}" apply -k deploy/base
"${K[@]}" apply -k deploy/alertmanager
"${K[@]}" apply -k deploy/recoverops
# Explicit restart because local image tag + ConfigMap contents may change.
"${K[@]}" rollout restart deployment/labgateway deployment/api-stable deployment/api-candidate deployment/prometheus deployment/alertmanager deployment/recoverops
for d in labgateway api-stable api-candidate prometheus alertmanager recoverops; do
 "${K[@]}" rollout status "deployment/$d" --timeout=90s
done
printf '%s\n' 'Setup applied. Register a healthy live template with scripts/register-recoverops.sh before injecting a fault.'
