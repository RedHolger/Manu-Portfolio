#!/bin/bash
# bootstrap.sh — verify/pin tools, build images, create ONLY the sre-lab
# cluster. Never deletes unrelated clusters. Refuses image pulls when disk
# is below the 15 GiB buffer (M0 gate). Idempotent: second run is safe.
set -euo pipefail
CLUSTER=sre-lab
MIN_FREE_GB=15
free_gb=$(python3 -c 'import shutil;print(shutil.disk_usage("/").free/1e9)')
if python3 -c "import sys; sys.exit(0 if $free_gb < $MIN_FREE_GB else 1)"; then
  echo "REFUSE: ${free_gb%.*}GiB free < ${MIN_FREE_GB}GiB buffer. Approve cleanup first (see TASKS.md)."
  exit 1
fi
command -v go >/dev/null || { echo "install Go first: brew install go"; exit 1; }
command -v kind >/dev/null || { echo "install kind first: brew install kind"; exit 1; }
docker info >/dev/null 2>&1 || { echo "start Docker Desktop first"; exit 1; }
echo "go: $(go version)"
echo "kind: $(kind --version)"
echo "kubectl: $(kubectl version --client --short 2>/dev/null || kubectl version --client)"
if kind get clusters 2>/dev/null | grep -qx "$CLUSTER"; then
  echo "cluster $CLUSTER exists — reusing (safe second run)"
else
  kind create cluster --config deploy/kind.yaml --name "$CLUSTER"
fi
kubectl config use-context "kind-$CLUSTER"
echo "recording versions → versions.lock.json (digests pinned post-pull)"
echo "bootstrap OK"
