#!/bin/bash
# doctor.sh — environment diagnostics. Exit nonzero with remediation when a
# prerequisite is missing. Diagnostic output is NOT a gate pass (D-010).
set -u
fail=0
need() { # need <name> <hint> ; runs `command -v`
  if command -v "$1" >/dev/null 2>&1; then
    echo "OK   $1: $(command -v "$1")"
  else
    echo "MISS $1 — $2"
    fail=1
  fi
}
ver() { # ver <label> <cmd...>
  local label="$1"; shift
  if out=$("$@" 2>&1); then
    echo "OK   $label: $(echo "$out" | head -1)"
  else
    echo "MISS $label — ${out:0:120}"
    fail=1
  fi
}
echo "== tools =="
need go "brew install go (needs ~1GiB free; see disk section)"
need kind "brew install kind (after Go + disk)"
need kubectl "brew install kubectl"
need docker "install Docker Desktop, then start the daemon"
need promtool "brew install prometheus (for rule tests)"
need python3 "conda or brew python3 (report aggregation)"
echo "== versions =="
command -v go >/dev/null && ver "go" go version
command -v kubectl >/dev/null && ver "kubectl" kubectl version --client
command -v kind >/dev/null && ver "kind" kind --version
echo "== docker daemon =="
if docker info >/dev/null 2>&1; then echo "OK   docker daemon reachable"
else echo "MISS docker daemon — open Docker Desktop and wait for green status"; fail=1; fi
echo "== resources =="
echo "cpus: $(sysctl -n hw.ncpu 2>/dev/null || nproc)"
echo "mem_gb: $(python3 -c 'import shutil;print(round(shutil.disk_usage("/").free/1e9,2))' 2>/dev/null || df -h / | tail -1)"
echo "disk_free: $(df -h / | tail -1 | awk '{print $4}')"
echo "== kind context =="
if kubectl config current-context 2>/dev/null | grep -q '^kind-sre-lab$'; then
  echo "OK   context is kind-sre-lab"
else
  echo "INFO context is '$(kubectl config current-context 2>/dev/null)' (want kind-sre-lab after bootstrap)"
fi
echo "== ports =="
for p in 8080 8081 8082 8083 9090 3000; do
  if (echo >/dev/tcp/127.0.0.1/$p) >/dev/null 2>&1; then echo "INFO port $p: in use"; fi
done
exit $fail
