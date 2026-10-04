#!/bin/bash
# Content-based rebuilds, architecture from the actual kind node, no stale binaries.
set -euo pipefail
./scripts/require-context.sh
./scripts/disk-floor.sh
ARCH=$(kubectl --context kind-sre-lab get node sre-lab-control-plane -o jsonpath='{.status.nodeInfo.architecture}')
case "$ARCH" in arm64|amd64) ;; *) echo "unsupported node architecture: $ARCH" >&2; exit 1;; esac
mkdir -p bin deploy/images/bin
for APP in labapi labgateway recoverops; do
  GOOS=linux GOARCH="$ARCH" CGO_ENABLED=0 go build -trimpath -o "deploy/images/bin/$APP" "./cmd/$APP"
  # exFAT may create AppleDouble sidecars that Docker cannot read through its
  # build-context xattr bridge.
  find deploy/images/bin -maxdepth 1 -type f -name '._*' -delete
  docker build --platform "linux/$ARCH" -f "deploy/images/Dockerfile.$APP" -t "sre-$APP:local" deploy/images/bin
  kind load docker-image "sre-$APP:local" --name sre-lab
  docker inspect --format='{{.Id}}' "sre-$APP:local"
done
for APP in labload budgetguard faultlab recoverops; do go build -trimpath -o "bin/$APP" "./cmd/$APP"; done
