#!/usr/bin/env bash
# build-demo-site.sh — regenerate the static demo data from the canonical Go
# CLI and the recorded evidence bundles. Nothing here invents numbers: every
# file under site/*/data is either CLI output or a read-only SQL dump of a
# committed results/ bundle.
#
#   scripts/build-demo-site.sh            # write site/ (and BUILD.json)
#   scripts/build-demo-site.sh --check    # regenerate elsewhere, fail on drift
set -euo pipefail
cd "$(dirname "$0")/.."

MODE="${1:-build}"
SITE_SRC=site
FIXTURE_END=2026-10-03T05:00:00Z
COMMIT="$(git rev-parse HEAD)"
COMMIT_SHORT="$(git rev-parse --short=12 HEAD)"
COMMIT_TIME="$(git log -1 --format=%cI)"
GO_VERSION="$(go version | awk '{print $3}')"

TMP=
if [ "$MODE" = "--check" ]; then
  TMP="$(mktemp -d)"
  trap 'rm -rf "$TMP"' EXIT
  OUT="$TMP/site"
  mkdir -p "$OUT"
  # HTML is hand-written source: compare it as-is.
  cp -R "$SITE_SRC"/. "$OUT"/
  rm -rf "$OUT/budgetguard/data" "$OUT/faultlab/data" "$OUT/recoverops/data"
else
  OUT="$SITE_SRC"
fi
mkdir -p "$OUT/budgetguard/data" "$OUT/faultlab/data" "$OUT/recoverops/data"

echo "build-demo-site: go build ./cmd/budgetguard"
go build -o bin/ ./cmd/budgetguard

echo "build-demo-site: budgetguard replay ×4 (fixtures)"
MANIFEST="$OUT/budgetguard/data/manifest.json"
{
  printf '{\n  "generated_by": "scripts/build-demo-site.sh",\n'
  printf '  "window_end": "%s",\n' "$FIXTURE_END"
  printf '  "runs": [\n'
  first=1
  for fx in healthy error slow thin; do
    out="$OUT/budgetguard/data/$fx.json"
    set +e
    ./bin/budgetguard replay \
      --fixture "tests/fixtures/releases/$fx.json" \
      --config configs/slos/reservations.yaml \
      --end "$FIXTURE_END" --out "$out" >/dev/null 2>&1
    code=$?
    set -e
    if [ "$first" = 0 ]; then printf ',\n'; fi
    first=0
    decision="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["decision"])' "$out")"
    reasons="$(python3 -c 'import json,sys; print(",".join(json.dumps(r) for r in json.load(open(sys.argv[1]))["reasons"]))' "$out")"
    printf '    {"fixture": "%s", "decision": "%s", "exit_code": %d, "reasons": [%s]}' \
      "$fx" "$decision" "$code" "$reasons"
  done
  printf '\n  ]\n}\n'
} > "$MANIFEST"

echo "build-demo-site: faultlab journals (read-only SQL dump)"
mkdir -p "$OUT/slos"
cp configs/slos/reservations.yaml "$OUT/slos/reservations.yaml"
python3 - "$OUT" <<'PY'
import json, sqlite3, sys, os, pathlib, shutil, tempfile
out = sys.argv[1]

def open_readonly(db_path):
    """Open a COPY: SQLite would otherwise drop -wal/-shm sidecars next to
    the evidence bundle it is reading."""
    tmp = tempfile.NamedTemporaryFile(suffix=".db", delete=False)
    tmp.close()
    shutil.copyfile(db_path, tmp.name)
    return sqlite3.connect(f"file:{tmp.name}?mode=ro", uri=True), tmp.name

def close(con, copy_path):
    con.close()
    try:
        os.unlink(copy_path)
    except OSError:
        pass

def dump(db_path, *, source, note):
    con, copy_path = open_readonly(db_path)
    con.row_factory = sqlite3.Row
    runs = [dict(r) for r in con.execute(
        "SELECT id,scenario_hash,service,state,started_at,deadline_at,seed,error FROM runs")]
    events = [dict(r) for r in con.execute(
        "SELECT sequence,timestamp,kind,payload_json FROM events ORDER BY sequence")]
    faults = [dict(r) for r in con.execute(
        "SELECT id,kind,target_json,expires_at,applied_at,cleared_at FROM faults ORDER BY id")]
    close(con, copy_path)
    return {
        "generated_by": "scripts/build-demo-site.sh",
        "source": source,
        "recorded_evidence": True,
        "note": note,
        "runs": runs, "events": events, "faults": faults,
    }

demos = [
    ("results/faultlab/demo2-expire/j.db", "passed",
     "Live kind-sre-lab run: 400ms/50% delay for 20s, PASSED with zero live faults after cleanup."),
    ("results/faultlab/demo6-abort/j.db", "aborted",
     "Live kind-sre-lab run: connection failure above the abort threshold; the runner cleaned up and recorded FAILED."),
]
for path, name, note in demos:
    if not os.path.exists(path):
        raise SystemExit(f"missing evidence bundle: {path}")
    payload = dump(path, source=path, note=note)
    pathlib.Path(out, "faultlab", "data", f"{name}.json").write_text(
        json.dumps(payload, indent=1, sort_keys=True) + "\n")

print("build-demo-site: recoverops incident (read-only SQL dump)")
ro_db = "results/recoverops/r3-live-20261003T151252Z/r3.db"
if not os.path.exists(ro_db):
    raise SystemExit(f"missing evidence bundle: {ro_db}")
con, ro_copy = open_readonly(ro_db)
con.row_factory = sqlite3.Row
incidents = []
for row in con.execute("SELECT id,state,policy_hash,created_at,updated_at,evidence FROM incidents ORDER BY created_at"):
    incidents.append(dict(row))
actions = []
for row in con.execute("SELECT action_key,incident_id,status,error,created_at,updated_at,"
                       "length(desired_hash) AS desired_len,length(before_hash) AS before_len "
                       "FROM actions ORDER BY created_at"):
    actions.append(dict(row))
events = [dict(r) for r in con.execute(
    "SELECT incident_id,seq,timestamp,kind,payload FROM events ORDER BY incident_id, seq")]
close(con, ro_copy)

# Drop raw template bodies: keep the hash/length only (no cluster payloads).
for e in events:
    try:
        p = json.loads(e["payload"])
    except Exception:
        e["payload"] = "{}"
        continue
    if isinstance(p, dict) and isinstance(p.get("desired"), str) and p["desired"].startswith("{"):
        p["desired"] = f"<template omitted: {len(p['desired'])} bytes>"
    e["payload"] = json.dumps(p, sort_keys=True)

r3_dir = "results/recoverops/r3-live-20261003T151252Z"
payload = {
    "generated_by": "scripts/build-demo-site.sh",
    "source": r3_dir,
    "recorded_evidence": True,
    "gate": "R3 ACCEPTED (see RELEASE_STATUS.md)",
    "note": "Live kind-sre-lab rollback: webhook to proposal to UID-pinned "
            "template-only patch to verification, with a separate observe-mode arm.",
    "incidents": incidents,
    "actions": actions,
    "events": events,
}
for name in ("execute", "verify"):
    src = os.path.join(r3_dir, f"{name}.json")
    if os.path.exists(src):
        payload[name] = json.loads(pathlib.Path(src).read_text())
pathlib.Path(out, "recoverops", "data", "r3-live.json").write_text(
    json.dumps(payload, indent=1, sort_keys=True) + "\n")
PY

echo "build-demo-site: BUILD.json"
cat > "$OUT/BUILD.json" <<JSON
{
  "commit": "$COMMIT",
  "commit_short": "$COMMIT_SHORT",
  "commit_time": "$COMMIT_TIME",
  "go_version": "$GO_VERSION",
  "generator": "scripts/build-demo-site.sh"
}
JSON

# AppleDouble sidecars (exFAT) must not ship with the site.
find "$OUT" -name '._*' -delete

if [ "$MODE" = "--check" ]; then
  # BUILD.json carries the generation-time commit on purpose: it is stamped,
  # not compared, so a commit after stamping cannot create false drift.
  if ! diff -r "$SITE_SRC/budgetguard/data" "$OUT/budgetguard/data" \
     || ! diff -r "$SITE_SRC/faultlab/data" "$OUT/faultlab/data" \
     || ! diff -r "$SITE_SRC/recoverops/data" "$OUT/recoverops/data"; then
    echo "build-demo-site: site data is out of date — run scripts/build-demo-site.sh" >&2
    exit 1
  fi
  echo "build-demo-site: check OK (site data matches the Go CLI and evidence bundles)"
else
  echo "build-demo-site: wrote $OUT"
fi
