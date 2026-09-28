#!/usr/bin/env python3
"""provision-grafana.py — create Prometheus datasource + SLO dashboard via API.
Usage: python3 scripts/provision-grafana.py [grafana-base-url]
Evidence: dashboard UID printed; screenshots taken separately with headless Chrome.
"""
import json
import sys
import urllib.request

BASE = sys.argv[1] if len(sys.argv) > 1 else "http://127.0.0.1:30300"
AUTH = ("admin", "admin")


def api(method, path, body=None):
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(BASE + path, data=data, method=method,
                                 headers={"Content-Type": "application/json"})
    import base64
    tok = base64.b64encode(f"{AUTH[0]}:{AUTH[1]}".encode()).decode()
    req.add_header("Authorization", "Basic " + tok)
    with urllib.request.urlopen(req, timeout=15) as r:
        return json.load(r)


def panel(pid, title, exprs, x, y, w=12, h=8, unit="short"):
    return {
        "id": pid, "title": title, "type": "timeseries",
        "gridPos": {"x": x, "y": y, "w": w, "h": h},
        "fieldConfig": {"defaults": {"unit": unit}, "overrides": []},
        "options": {"legend": {"displayMode": "table", "placement": "right"}},
        "targets": [{"refId": chr(65 + i), "expr": e,
                     "datasource": {"type": "prometheus", "uid": DS_UID}}
                    for i, e in enumerate(exprs)],
    }


# 1. Datasource (idempotent-ish: delete-then-create would drop uid; reuse by name).
try:
    dss = api("GET", "/api/datasources")
    DS_UID = next(d["uid"] for d in dss if d["name"] == "Prometheus")
    print("datasource exists:", DS_UID)
except StopIteration:
    ds = api("POST", "/api/datasources", {
        "name": "Prometheus", "type": "prometheus", "access": "proxy",
        "url": "http://prometheus.sre-lab.svc:9090", "isDefault": True})
    DS_UID = ds["datasource"]["uid"]
    print("datasource created:", DS_UID)

panels = [
    panel(1, "Eligible RPS by slot",
          ['sum by (slot) (rate(lab_requests_total{service="reservations"}[1m]))'],
          0, 0, unit="reqps"),
    panel(2, "Availability bad ratio (5m)",
          ["service:availability_bad_ratio:5m"], 12, 0, unit="percentunit"),
    panel(3, "Latency bad ratio (5m)",
          ["service:latency_bad_ratio:5m"], 0, 8, unit="percentunit"),
    panel(4, "Burn rate — availability 1h vs budget",
          ["service:availability_bad_ratio:1h / 0.001"], 12, 8),
    panel(5, "Telemetry freshness (seconds since last scrape)",
          ['time() - max(timestamp(lab_requests_total{service="reservations"}))'],
          0, 16, unit="s"),
]
dash = {"uid": "sre-slo", "title": "SRE SLO — reservations",
        "tags": ["sre", "slo"], "timezone": "utc",
        "schemaVersion": 39, "version": 1, "panels": panels}
res = api("POST", "/api/dashboards/db", {"dashboard": dash, "overwrite": True})
print("dashboard:", res.get("uid"), res.get("url"))
