#!/usr/bin/env python3
# compare-report.py — paired baseline-versus-resilient report from one
# compare-<TS> run directory. Reads metas, JSONL attempts, summaries and
# oracle console lines; writes report.md. Attempts vs logical operations:
# labload JSONL holds one line per LOGICAL op (final attempt only);
# attempts = lines + lines with attempt==2 (at most one retry each).
# Invalid runs (load invalid, nonzero exit, oracle violations) are listed
# and excluded from aggregate means, never deleted.
import json, os, re, sys
from datetime import datetime, timezone

OUT = sys.argv[1]
runs = {}
for name in sorted(os.listdir(OUT)):
    d = os.path.join(OUT, name)
    m = os.path.join(d, "meta.json")
    if not os.path.isfile(m):
        continue
    meta = json.load(open(m))
    if meta.get("status") == "reseed-failed":
        runs[name] = {"meta": meta, "invalid": "reseed-failed"}
        continue
    s = meta["summary"]
    atts = [json.loads(l) for l in open(os.path.join(d, "load.jsonl"))]
    logical = len(atts)
    good = sum(1 for a in atts if 200 <= a["code"] < 300)
    retries = sum(1 for a in atts if a.get("attempt") == 2)
    attempts = logical + retries
    lats = sorted(a["latency_ms"] for a in atts)
    def pct(p):
        return lats[min(len(lats) - 1, p * len(lats) // 100)] if lats else 0.0
    ec = {}
    for a in atts:
        ec[a.get("err_class", "?")] = ec.get(a.get("err_class", "?"), 0) + 1
    # Recovery: pre-fault goodput vs first recovered 10s window after clear.
    t0, ta, tc = meta["t_start"], meta["t_arm"], meta["t_clear"]
    # planned_at is ISO; parse once:
    def ts(a):
        return datetime.fromisoformat(a["planned_at"].replace("Z", "+00:00")).timestamp()
    pre = [a for a in atts if t0 + 5 <= ts(a) < ta and 200 <= a["code"] < 300]
    g_pre = len(pre) / max(1.0, ta - t0 - 5)
    rec = None
    for w in range(0, 31):
        win = [a for a in atts if tc + w <= ts(a) < tc + w + 10 and 200 <= a["code"] < 300]
        if len(win) / 10.0 >= 0.9 * g_pre and g_pre > 0:
            rec = w
            break
    # Oracle outcome from the console line.
    oline = [l for l in open(os.path.join(d, "console.log")) if "oracle CLEAN" in l or "oracle VIOLATED" in l]
    verdict, committed, oacked, oamb, oviol = "UNKNOWN", -1, -1, -1, -1
    if oline:
        m2 = re.search(r"oracle (CLEAN|VIOLATED): committed=(\d+) acked=(\d+) ambiguous=(\d+) violations=(\d+)", oline[-1])
        if m2:
            verdict, committed, oacked, oamb, oviol = m2.group(1), *map(int, m2.groups()[1:])
    invalid = []
    if meta["labload_exit"] != 0:
        invalid.append(f"labload exit {meta['labload_exit']}")
    if not meta["load_valid"]:
        invalid.append("load invalid (drops)")
    if verdict == "VIOLATED":
        invalid.append("oracle violations")
    if verdict == "UNKNOWN":
        invalid.append("oracle outcome missing")
    runs[name] = {"meta": meta, "offered": s.get("Offered"), "dropped": s.get("Dropped"),
                  "achieved_rps": round(s.get("AchievedRPS", 0), 2),
                  "logical": logical, "good": good,
                  "fail_ratio": round(1 - good / max(1, logical), 4),
                  "p50": round(pct(50), 1), "p95": round(pct(95), 1), "p99": round(pct(99), 1),
                  "attempts": attempts, "retries": retries,
                  "amplification": round(attempts / max(1, logical), 3),
                  "errclass": ec, "recovery_s": rec if rec is not None else ">30",
                  "oracle": verdict, "pg_committed": committed,
                  "o_ambiguous": oamb, "invalid": "; ".join(invalid)}

pairs = {}
for name, r in runs.items():
    m = r["meta"]
    if m.get("status") == "reseed-failed":
        continue
    pairs.setdefault((m["fault"], m["seed"]), {})[m["profile"]] = (name, r)

L = []
L.append("# Baseline-versus-resilient paired comparison")
L.append("")
L.append(f"source: `{os.path.basename(OUT)}`; profiles differ only in `--correctness-profile` (D-011).")
L.append("attempts = logical ops + retries (JSONL holds one line per logical op, final attempt only).")
L.append("")
for (fault, seed) in sorted(pairs):
    p = pairs[(fault, seed)]
    L.append(f"## fault={fault} seed={seed}")
    for prof in ("baseline", "resilient"):
        if prof not in p:
            L.append(f"- {prof}: MISSING RUN")
            continue
        name, r = p[prof]
        L.append(f"- {prof} `{name}`: offered={r['offered']} dropped={r['dropped']} "
                 f"achieved={r['achieved_rps']}rps logical={r['logical']} good={r['good']} "
                 f"fail={r['fail_ratio']} p50/p95/p99={r['p50']}/{r['p95']}/{r['p99']}ms "
                 f"attempts={r['attempts']} retries={r['retries']} amp={r['amplification']} "
                 f"err={r['errclass']} recovery={r['recovery_s']}s pg_committed={r['pg_committed']} "
                 f"ambiguous={r['o_ambiguous']} oracle={r['oracle']}"
                 + (f" INVALID({r['invalid']})" if r["invalid"] else ""))
    if "baseline" in p and "resilient" in p:
        b, r = p["baseline"][1], p["resilient"][1]
        if not b["invalid"] and not r["invalid"]:
            L.append(f"- delta (resilient-baseline): fail {r['fail_ratio']-b['fail_ratio']:+.4f}, "
                     f"good {r['good']-b['good']:+d}, p99 {r['p99']-b['p99']:+.1f}ms, "
                     f"amp {r['amplification']-b['amplification']:+.3f}, "
                     f"recovery {r['recovery_s']}s vs {b['recovery_s']}s, "
                     f"pg_committed {r['pg_committed']-b['pg_committed']:+d}")
    L.append("")

L.append("## Aggregates (valid pairs only)")
for fault in sorted({f for f, _ in pairs}):
    ok = [(pairs[(fault, s)]["baseline"][1], pairs[(fault, s)]["resilient"][1])
          for s in sorted({sd for f, sd in pairs if f == fault})
          if "baseline" in pairs[(fault, s)] and "resilient" in pairs[(fault, s)]
          and not pairs[(fault, s)]["baseline"][1]["invalid"]
          and not pairs[(fault, s)]["resilient"][1]["invalid"]]
    if not ok:
        L.append(f"- {fault}: no valid pairs")
        continue
    n = len(ok)
    for prof, idx in (("baseline", 0), ("resilient", 1)):
        mg = sum(r[idx]["good"] for r in ok) / n
        mf = sum(r[idx]["fail_ratio"] for r in ok) / n
        mp = sum(r[idx]["p99"] for r in ok) / n
        ma = sum(r[idx]["amplification"] for r in ok) / n
        L.append(f"- {fault} {prof} (n={n}): mean good={mg:.1f} fail={mf:.4f} p99={mp:.1f}ms amp={ma:.3f}")
    df = sum(r[1]["fail_ratio"] - r[0]["fail_ratio"] for r in ok) / n
    dg = sum(r[1]["good"] - r[0]["good"] for r in ok) / n
    da = sum(r[1]["amplification"] - r[0]["amplification"] for r in ok) / n
    dp = sum(r[1]["p99"] - r[0]["p99"] for r in ok) / n
    regs = []
    if df > 0:
        regs.append("higher failure ratio")
    if dg < 0:
        regs.append("fewer good ops")
    if dp > 0:
        regs.append(f"higher tail latency (p99 {dp:+.1f}ms)")
    L.append(f"- {fault} mean delta: fail {df:+.4f}, good {dg:+.1f}, p99 {dp:+.1f}ms, amp {da:+.3f}")
    L.append(f"- {fault} regressions under resilient: " + ("; ".join(regs) if regs else "none"))
L.append("")
open(os.path.join(OUT, "report.md"), "w").write("\n".join(L) + "\n")
print(f"wrote {OUT}/report.md ({len(pairs)} pairs)")
