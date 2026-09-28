#!/usr/bin/env python3
"""report.py — aggregate a results/<run-id>/ dir into report.md (stdlib only).
Fails loudly when expected artifacts are absent; a report failure never
masks an experiment failure (both statuses preserved, exit 1).
Usage: python3 scripts/report.py results/budgetguard/<run-id>
"""
import json
import sys
from pathlib import Path

EXPECTED = ["healthy.json", "error.json", "slow.json"]


def main() -> int:
    if len(sys.argv) != 2:
        print("usage: report.py <run-dir>", file=sys.stderr)
        return 1
    d = Path(sys.argv[1])
    if not d.is_dir():
        print(f"missing run dir {d}", file=sys.stderr)
        return 1
    rows = []
    failed = []
    for name in EXPECTED:
        p = d / name
        if not p.exists():
            failed.append(name)
            continue
        try:
            r = json.loads(p.read_text())
            rows.append((name, r.get("decision"), ",".join(r.get("reasons", [])),
                         r.get("candidate", {}), r.get("stable", {})))
        except json.JSONDecodeError as e:
            failed.append(f"{name} (bad json: {e})")
    lines = ["# BudgetGuard run report", "", f"run: `{d}`", "",
             "| case | decision | reasons | cand eligible/bad/slow | stable eligible/bad/slow |",
             "|---|---|---|---|---|"]
    for name, dec, rs, c, s in rows:
        lines.append(f"| {name} | {dec} | {rs} | "
                     f"{c.get('eligible')}/{c.get('bad')}/{c.get('slow_or_bad')} | "
                     f"{s.get('eligible')}/{s.get('bad')}/{s.get('slow_or_bad')} |")
    if failed:
        lines += ["", "## MISSING/UNREADABLE (experiment failure preserved)"]
        lines += [f"- {f}" for f in failed]
    (d / "report.md").write_text("\n".join(lines) + "\n")
    print("\n".join(lines))
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())
