#!/bin/bash
# build-images.sh — static local builds + kind load + digest pinning.
# No SDK image pull (CGO_ENABLED=0 local build, scratch runtime).
set -euo pipefail
export PATH="/opt/homebrew/bin:$PATH"
command -v kind >/dev/null || { echo "install kind first"; exit 1; }
kind get clusters 2>/dev/null | grep -qx sre-lab || { echo "run make bootstrap first"; exit 1; }
mkdir -p deploy/images/bin
# Rebuild avoidance: skip when binaries are newer than all Go sources.
# (Unnecessary rebuilds churn Docker build cache on a tight disk.)
if [ -f deploy/images/bin/labapi ] && [ -f deploy/images/bin/labgateway ] \
  && [ -z "$(find cmd internal go.mod go.sum -type f -newer deploy/images/bin/labapi 2>/dev/null)" ] \
  && [ -z "$(find cmd internal go.mod go.sum -type f -newer deploy/images/bin/labgateway 2>/dev/null)" ]; then
  echo "binaries newer than sources — skipping rebuild (rm deploy/images/bin/* to force)"
else
# Cross-compile for the kind node (linux/arm64): host darwin binaries fail
# in-container with "exec format error" (caught live 2026-09-27).
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -o deploy/images/bin/labapi ./cmd/labapi
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -o deploy/images/bin/labgateway ./cmd/labgateway
fi
docker build -f deploy/images/Dockerfile.labapi -t sre-labapi:local deploy/images/bin
docker build -f deploy/images/Dockerfile.labgateway -t sre-labgateway:local deploy/images/bin
kind load docker-image sre-labapi:local sre-labgateway:local --name sre-lab
echo "--- digests (record into versions.lock.json) ---"
docker inspect --format='sre-labapi:local {{.Id}}' sre-labapi:local
docker inspect --format='sre-labgateway:local {{.Id}}' sre-labgateway:local
