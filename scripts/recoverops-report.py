#!/usr/bin/env python3
"""Report all outcomes; never manufacture a recovery time for unresolved arms."""
import argparse, json, pathlib, statistics, math

def qualifies(arms):
    if set(arms)!={'controller','baseline'}: return False
    return (all(a.get('schema_version')==3 and a.get('status')=='VALID' and a.get('verification')
                and isinstance(a.get('verified_recovery_seconds'),(int,float))
                and math.isfinite(a['verified_recovery_seconds']) and a['verified_recovery_seconds']>=0
                for a in arms.values())
            and arms['controller'].get('seed') is not None
            and arms['controller']['seed']==arms['baseline'].get('seed'))

def summarize(root):
    rows=[];delta=[];valid=0;seen=set()
    for p in sorted(root.glob('pair-*')):
        arms={}
        for name in ('controller','baseline'):
            f=p/name/'result.json'
            arms[name]=json.loads(f.read_text()) if f.exists() else {'status':'MISSING'}
        ok=qualifies(arms)
        if ok:
            seed=arms['controller']['seed'];ok=seed not in seen;seen.add(seed)
        if ok:
            valid+=1;delta.append(arms['baseline']['verified_recovery_seconds']-arms['controller']['verified_recovery_seconds'])
        rows.append((p.name,arms,ok))
    pilot_arms={}
    for name in ('controller','baseline'):
        f=root/'pilot'/name/'result.json'
        if f.exists():pilot_arms[name]=json.loads(f.read_text())
    pilot_ok=qualifies(pilot_arms)
    return rows,delta,valid,pilot_ok

def main():
    ap=argparse.ArgumentParser();ap.add_argument('root',type=pathlib.Path);ap.add_argument('--require-complete',action='store_true');a=ap.parse_args()
    rows,delta,valid,pilot=summarize(a.root)
    lines=['# RecoverOps measured matrix','',f'Valid pilot: {pilot}. Valid measured pairs: {valid}/10.','',
           'Baseline uses an actual 120-second execution delay from receipt; this is not a study of human response times. Recovery time includes rollout and three measured healthy windows.','',
           '| Pair | Controller | Baseline | Counts |','|---|---|---|---|']
    for name,arms,ok in rows:lines.append(f"| {name} | {arms['controller']['status']} | {arms['baseline']['status']} | {ok} |")
    if delta:lines+=['',f'Measured paired baseline-minus-controller recovery seconds: median {statistics.median(delta):.3f}; range {min(delta):.3f}–{max(delta):.3f}. n={len(delta)}.']
    else:lines+=['','No qualifying measured recovery comparison yet.']
    (a.root/'report.md').write_text('\n'.join(lines)+'\n')
    print('\n'.join(lines))
    if a.require_complete and not (pilot and valid==10 and len(rows)==10):raise SystemExit(2)
if __name__=='__main__':main()
