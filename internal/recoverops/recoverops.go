// Package recoverops — durable incident remediation controller (R1:
// ingestion and durable state). R1 receives Alertmanager webhooks,
// persists incidents and alert occurrences idempotently in SQLite, and
// exposes read/cancel endpoints. R1 performs NO cluster mutation.
package recoverops
