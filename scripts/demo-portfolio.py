#!/usr/bin/env python3
"""Sequential live demo. Owns only this process's forwards/load; preserves artifacts."""
import argparse, datetime as dt, fcntl, importlib.util, json, os, pathlib, signal, subprocess, time
spec=importlib.util.spec_from_file_location('experiment',pathlib.Path(__file__).with_name('recoverops-experiment.py'))
e=importlib.util.module_from_spec(spec);spec.loader.exec_module(e)

def route(d, percent):
 state=e.http(18082,'/admin/state',d.admin)
 e.http(18082,'/admin/routing',d.admin,{'version':state['routingVersion'],'candidatePercent':percent},'PUT')

def child(d,args,path,timeout):
 log=path.open('w');d.files.append(log)
 p=subprocess.Popen(args,stdout=log,stderr=log);d.children.append(p)
 d.wait(lambda:p.poll() is not None,timeout,'demo subprocess')
 return p.returncode

def budgetguard(d,out):
 original=json.loads(e.kubectl('get','deployment','api-candidate','-o','json'))
 e.save(out/'candidate-before.json',original)
 current=None;result=[]
 try:
  for i,(mode,expected) in enumerate([('healthy',0),('error',2),('slow',2)]):
   route(d,0)
   live=json.loads(e.kubectl('get','deployment','api-candidate','-o','json'))
   if live['metadata']['uid']!=original['metadata']['uid']:raise RuntimeError('candidate replaced')
   if current is not None and live['spec']['template']!=current:raise RuntimeError('operator changed candidate')
   current=json.loads(json.dumps(original['spec']['template']))
   c=next(c for c in current['spec']['containers'] if c['name']=='labapi')
   c['args']=[a for a in c.get('args',[]) if not a.startswith(('-mode=','-error-rate=','-slow-ms='))]+['-mode='+mode,'-error-rate=0.05','-slow-ms=400']
   patch=[{'op':'test','path':'/metadata/uid','value':live['metadata']['uid']},{'op':'test','path':'/metadata/resourceVersion','value':live['metadata']['resourceVersion']},{'op':'replace','path':'/spec/template','value':current}]
   e.kubectl('patch','deployment','api-candidate','--type=json','-p',json.dumps(patch))
   e.kubectl('rollout','status','deployment/api-candidate','--timeout=90s');route(d,20)
   case=out/mode;case.mkdir(parents=True)
   code=child(d,[str(e.ROOT/'bin/labload'),'run','--gateway','http://127.0.0.1:18080','--rate','25','--duration','330s','--seed',str(700+i),'--key-prefix',out.name+'-'+mode,'--output',str(case/'traffic.jsonl')],case/'load.log',350)
   if code:raise RuntimeError('invalid BudgetGuard workload')
   # Last 300 seconds are isolated from the preceding case by a 330s workload.
   end=dt.datetime.now(dt.timezone.utc).replace(microsecond=0);start=end-dt.timedelta(seconds=300)
   code=child(d,[str(e.ROOT/'bin/budgetguard'),'evaluate','--config','configs/slos/reservations.yaml','--prometheus','http://127.0.0.1:19090','--start',start.isoformat(),'--end',end.isoformat(),'--out',str(case/'decision.json')],case/'evaluate.log',60)
   result.append({'case':mode,'exit':code,'expected':expected});e.save(out/'cases.json',result)
   if code!=expected:raise RuntimeError(f'BudgetGuard {mode}: exit {code}, wanted {expected}')
  return result
 finally:
  # Restore only our exact last template, retaining any concurrent operator edit.
  try:
   route(d,0)
   live=json.loads(e.kubectl('get','deployment','api-candidate','-o','json'))
   if current is not None and live['metadata']['uid']==original['metadata']['uid'] and live['spec']['template']==current:
    e.kubectl('patch','deployment','api-candidate','--type=json','-p',json.dumps([{'op':'test','path':'/metadata/resourceVersion','value':live['metadata']['resourceVersion']},{'op':'test','path':'/metadata/uid','value':live['metadata']['uid']},{'op':'replace','path':'/spec/template','value':original['spec']['template']}]))
    e.kubectl('rollout','status','deployment/api-candidate','--timeout=90s')
   e.save(out/'cleanup.json',{'routing':e.http(18082,'/admin/state',d.admin),'candidate':json.loads(e.kubectl('get','deployment','api-candidate','-o','json'))})
  except Exception as err:
   e.save(out/'cleanup-error.json',{'error':str(err)});raise

def main():
 ap=argparse.ArgumentParser();ap.add_argument('--out',type=pathlib.Path,required=True);ap.add_argument('--product',choices=['all','budgetguard'],default='all');a=ap.parse_args()
 os.chdir(e.ROOT);out=a.out.resolve()
 if out.exists():raise RuntimeError('use a new output directory; existing evidence is preserved')
 out.mkdir(parents=True);(e.ROOT/'results').mkdir(exist_ok=True)
 lock=(e.ROOT/'results/.recoverops-experiment.lock').open('a');fcntl.flock(lock,fcntl.LOCK_EX|fcntl.LOCK_NB)
 e.command(['./scripts/require-context.sh']);e.floor(out)
 d=e.Driver(out);replicas=None
 record={'schema_version':3,'started_at':e.utc(),'status':'RUNNING','commit':e.command(['git','rev-parse','HEAD']).strip(),'stages':{}}
 e.save(out/'result.json',record)
 signal.signal(signal.SIGTERM,lambda *_:(_ for _ in ()).throw(KeyboardInterrupt()))
 try:
  d.connect(require_controller=a.product=='all');d.capacity(50000)
  raw=e.kubectl('get','deployment','recoverops','--ignore-not-found','-o','json')
  if raw.strip():
   obj=json.loads(raw)
   if obj['spec'].get('replicas',1)!=1:raise RuntimeError('requires one healthy controller replica')
   if d.ro('/v1/limits')['active']:raise RuntimeError('active remediation; demo refused')
   replicas=1
   e.kubectl('scale','deployment/recoverops','--replicas=0')
   e.kubectl('wait','--for=delete','pod','-l','app=recoverops','--timeout=90s')
  record['stages']['budgetguard']=budgetguard(d,out/'budgetguard')
  if a.product=='budgetguard':
   record['status']='PASSED'
   return
  fl=out/'faultlab';fl.mkdir()
  # Separate this run's logical keys from all prior demo runs, preserving source scenario.
  scenario=(e.ROOT/'configs/faults/delay.yaml').read_text().replace('seed: 42','seed: '+str(time.time_ns()%1000000000))
  (fl/'scenario.yaml').write_text(scenario)
  code=child(d,[str(e.ROOT/'bin/faultlab'),'run','--scenario',str(fl/'scenario.yaml'),'--out',str(fl),'--db',str(fl/'journal.db'),'--gateway','http://127.0.0.1:18082','--metrics','http://127.0.0.1:18080'],fl/'run.log',260)
  result=json.loads((fl/'result.json').read_text());record['stages']['faultlab']=result
  if code or result.get('terminal')!='PASSED':raise RuntimeError('FaultLab experiment/cleanup did not pass')
  if e.http(18082,'/admin/state',d.admin).get('faults'):raise RuntimeError('fault remains after experiment')
  e.kubectl('scale','deployment/recoverops',f'--replicas={replicas}')
  e.kubectl('rollout','status','deployment/recoverops','--timeout=90s')
  d.wait(lambda:d.ro('/v1/limits'),60,'controller resumed')
  record['stages']['recoverops']=d.arm(out/'recoverops',900,'controller')
  record['status']='PASSED'
 except BaseException as err:
  record['status']='FAILED';record['error']=str(err);raise
 finally:
  record['finished_at']=e.utc();e.save(out/'result.json',record)
  try:
   if replicas is not None:
    e.kubectl('scale','deployment/recoverops',f'--replicas={replicas}')
    e.kubectl('rollout','status','deployment/recoverops','--timeout=90s')
  except Exception as err:
   record['status']='FAILED';record['cleanup_error']=str(err);e.save(out/'result.json',record);raise
  finally:
   d.close();lock.close()
   if (out/'STOP').exists():
    record['status']='FAILED';record['cleanup_error']='STOP preserved; cleanup unverified';e.save(out/'result.json',record)
    raise RuntimeError(record['cleanup_error'])
 print('COMPLETE',out,flush=True)
if __name__=='__main__':main()
