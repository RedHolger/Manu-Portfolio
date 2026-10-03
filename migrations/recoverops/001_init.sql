-- 001_init.sql — RecoverOps durable state (R1). SQLite. All writes go
-- through the Store with single-writer serialization; reopen must preserve
-- every row (R1 gate: real database reopen tests).
CREATE TABLE IF NOT EXISTS incidents (
  id TEXT PRIMARY KEY,
  source_key TEXT NOT NULL UNIQUE,
  target_uid TEXT NOT NULL DEFAULT '',
  policy_hash TEXT NOT NULL DEFAULT '',
  state TEXT NOT NULL,
  evidence TEXT NOT NULL DEFAULT '{}',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

-- One row per webhook delivery occurrence. delivery_id dedupes exact
-- redeliveries; occurrence_id groups firing/resolved pairs.
CREATE TABLE IF NOT EXISTS alert_events (
  delivery_id TEXT PRIMARY KEY,
  occurrence_id TEXT NOT NULL,
  incident_id TEXT REFERENCES incidents(id),
  firing INTEGER NOT NULL,
  starts_at TEXT NOT NULL,
  ends_at TEXT NOT NULL DEFAULT '',
  received_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_alert_events_occ ON alert_events(occurrence_id);

CREATE TABLE IF NOT EXISTS actions (
  action_key TEXT PRIMARY KEY,
  incident_id TEXT NOT NULL REFERENCES incidents(id),
  before_hash TEXT NOT NULL DEFAULT '',
  desired_hash TEXT NOT NULL DEFAULT '',
  resource_version TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL,
  error TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS known_good (
  target_uid TEXT PRIMARY KEY,
  template_json TEXT NOT NULL,
  template_hash TEXT NOT NULL,
  verified_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS events (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  incident_id TEXT NOT NULL REFERENCES incidents(id),
  seq INTEGER NOT NULL,
  timestamp TEXT NOT NULL,
  kind TEXT NOT NULL,
  payload TEXT NOT NULL DEFAULT '{}',
  UNIQUE (incident_id, seq)
);

-- Durable operational mode + schema marker (restart must not reset limits
-- or mode in R2; R1 persists them from the start).
CREATE TABLE IF NOT EXISTS meta (
  k TEXT PRIMARY KEY,
  v TEXT NOT NULL
);
