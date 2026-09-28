# Runbook — lab + BudgetGuard

## Bring-up (after M0 unblocked)
```sh
make doctor          # expect all OK; anything MISS blocks below
make bootstrap       # creates kind-sre-lab ONLY
make test && make test-race && make lint
make lab-up && make smoke
```

## Compile + test rules
```sh
go run ./cmd/budgetguard validate --config configs/slos/reservations.yaml
go run ./cmd/budgetguard compile --config configs/slos/reservations.yaml \
  --out monitoring/generated-rules.yaml \
  --demo-out monitoring/generated-rules-demo.yaml
make test-rules      # promtool check + fixture evaluation
```

## Live evaluation
```sh
END=$(date -u +%Y-%m-%dT%H:%M:%SZ)
go run ./cmd/budgetguard evaluate --config configs/slos/reservations.yaml \
  --prometheus http://127.0.0.1:9090 --start <RFC3339> --end "$END" \
  --out results/release.json   # exit 0/2/3
```

## Offline replay
```sh
go run ./cmd/budgetguard replay --config configs/slos/reservations.yaml \
  --fixture tests/fixtures/releases/error.json --end "$END" --out results/replay.json
```

## Teardown (only the named lab)
```sh
make lab-down
```

## Resume from paused state (scaled-to-0, Docker restarted)
The lab is currently paused: cluster `sre-lab` exists, all deployments at 0
replicas, images loaded, `pgdata` PVC retained, secrets in etcd. To resume:
```sh
open -a Docker                                      # wait for daemon
MIN_GB=2 ./scripts/disk-floor.sh                    # refuse if below floor
kubectl config use-context kind-sre-lab
kubectl -n sre-lab scale deploy/api-stable deploy/api-candidate --replicas=2
kubectl -n sre-lab scale deploy/labgateway deploy/postgres deploy/prometheus --replicas=1
./scripts/wait-ready.sh
# Grafana keeps no state across pod recreation (sqlite on container fs):
kubectl -n sre-lab scale deploy/grafana --replicas=1
kubectl -n sre-lab port-forward svc/grafana 13030:3000 &
python3 scripts/provision-grafana.py http://127.0.0.1:13030
# Secrets survive stop/start (etcd in node container). If the cluster was
# DELETED (not paused), recreate: namespace, postgres-pass + postgres-url +
# lab-admin secrets (random values, never commit), prometheus-rules ConfigMap
# from monitoring/generated-rules.yaml, then deploy/base.
```
Dashboard provisioning is required after every Grafana pod recreation;
verify with `GET /api/dashboards/uid/sre-slo` before screenshotting.
