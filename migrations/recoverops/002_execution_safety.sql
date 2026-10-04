
CREATE TABLE IF NOT EXISTS execution_intents (
 incident_id TEXT PRIMARY KEY REFERENCES incidents(id),
 target TEXT NOT NULL, uid TEXT NOT NULL, before_hash TEXT NOT NULL,
 desired_hash TEXT NOT NULL, template_json TEXT NOT NULL,
 resource_version TEXT NOT NULL, started_at TEXT NOT NULL,
 executed_at TEXT NOT NULL DEFAULT '', attempts INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS known_good_bindings (
 target TEXT PRIMARY KEY, uid TEXT NOT NULL, template_hash TEXT NOT NULL,
 verified_at TEXT NOT NULL
);
