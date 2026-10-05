#!/bin/bash
# Explicit, additive lab stock top-up; no rows deleted or history reset.
set -euo pipefail
./scripts/require-context.sh
./scripts/disk-floor.sh
MINIMUM="${1:?usage: prepare-stock.sh MINIMUM_AVAILABLE}"
case "$MINIMUM" in *[!0-9]*|'') echo 'positive integer required' >&2; exit 1;; esac
[ "$MINIMUM" -gt 0 ] && [ "$MINIMUM" -le 1000000 ] || { echo 'range 1..1000000' >&2; exit 1; }
kubectl --context kind-sre-lab -n sre-lab exec -i deploy/postgres -- psql -U lab -d lab -v ON_ERROR_STOP=1 <<SQL
BEGIN;
LOCK TABLE reservations IN SHARE MODE;
LOCK TABLE inventory IN SHARE ROW EXCLUSIVE MODE;
DO \$\$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM inventory WHERE sku='demo-item') THEN RAISE EXCEPTION 'demo-item missing'; END IF;
 IF EXISTS(SELECT 1 FROM inventory WHERE sku='demo-item' AND initial_stock-available<>(SELECT COALESCE(sum(quantity),0) FROM reservations WHERE sku='demo-item')) THEN RAISE EXCEPTION 'inventory invariant drift; stop and investigate'; END IF;
END \$\$;
UPDATE inventory SET initial_stock=initial_stock+GREATEST(0,$MINIMUM-available), available=GREATEST(available,$MINIMUM) WHERE sku='demo-item';
SELECT sku, initial_stock, available, initial_stock-available-(SELECT COALESCE(sum(quantity),0) FROM reservations WHERE sku='demo-item') AS invariant_drift FROM inventory WHERE sku='demo-item';
COMMIT;
SQL
