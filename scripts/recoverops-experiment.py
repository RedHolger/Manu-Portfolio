#!/usr/bin/env python3
"""Measured, resumable RecoverOps pairs. Never counts prior-schema evidence.
Requires setup-recoverops.sh and RECOVEROPS_TOKEN + LAB_ADMIN_TOKEN in environment.
The baseline differs only by a durable 120s execution hold configured BEFORE fault.
"""
import argparse, datetime as dt, fcntl, hashlib, json, os, pathlib, shutil
import signal, subprocess, sys, time, urllib.request, urllib.error

CTX, NS = 'kind-sre-lab', 'sre-lab'
TERMINAL = {'RESOLVED', 'ESCALATED', 'SUPPRESSED', 'CANCELLED'}
ROOT = pathlib.Path(__file__).resolve().parents[1]

def utc(): return dt.datetime.now(dt.timezone.utc).isoformat()
def stamp(s): return dt.datetime.fromisoformat(s.replace('Z', '+00:00')).timestamp()
def save(p, obj):
    p = pathlib.Path(p); p.parent.mkdir(parents=True, exist_ok=True)
    tmp = p.with_suffix(p.suffix + '.tmp'); tmp.write_text(json.dumps(obj, indent=2) + '\n'); tmp.replace(p)
def command(args, timeout=60, data=None):
    return subprocess.run(args, input=data, text=True, capture_output=True,
                          timeout=timeout, check=True).stdout

def kubectl(*args, data=None):
    return command(['kubectl', '--context', CTX, '-n', NS, *args], 100, data)

def floor(out):
    if (out/'STOP').exists(): raise RuntimeError('STOP exists; investigate before resuming')
    for path in ('/', str(ROOT)):
        if shutil.disk_usage(path).free < 2*1024**3:
            (out/'STOP').touch(); raise RuntimeError('2GiB floor reached')

def http(port, path, token='', body=None, method=None):
    headers = {'Content-Type': 'application/json'}
    if token: headers['Authorization'] = 'Bearer '+token
    data = None if body is None else json.dumps(body).encode()
    req = urllib.request.Request(f'http://127.0.0.1:{port}'+path, data=data, headers=headers, method=method)
    with urllib.request.urlopen(req, timeout=10) as r:
        raw=r.read(); return json.loads(raw) if raw else {}

class Driver:
    def __init__(self, out):
        self.out=out; self.children=[]; self.files=[]; self.forwards={}; self.controller_original=None; self.controller_owned=None
        self.token=os.environ.get('RECOVEROPS_TOKEN','').strip()
        self.admin=os.environ['LAB_ADMIN_TOKEN'].strip()
        if not self.admin: raise RuntimeError('empty admin token')
    def guard(self): floor(self.out)
    def wait(self, predicate, seconds, label):
        deadline=time.monotonic()+seconds; last=None; heartbeat=0
        while time.monotonic()<deadline:
            self.guard()
            try:
                v=predicate()
                if v: return v
            except (urllib.error.URLError, TimeoutError, ConnectionError, json.JSONDecodeError) as e: last=type(e).__name__
            if time.monotonic()>heartbeat:
                print(utc(),label, last or '', flush=True); heartbeat=time.monotonic()+30
            time.sleep(5)
        raise RuntimeError(f'{label}: deadline exceeded ({last})')
    def forward(self, port, resource, target):
        p=self.forwards.get(port)
        if p and p.poll() is None: return
        logpath=self.out/f'forward-{port}.log'
        offset=logpath.stat().st_size if logpath.exists() else 0
        log=logpath.open('a');self.files.append(log)
        p=subprocess.Popen(['kubectl','--context',CTX,'-n',NS,'port-forward',resource,f'{port}:{target}'],stdout=log,stderr=log)
        self.children.append(p);self.forwards[port]=p
        for _ in range(20):
            self.guard()
            if p.poll() is not None:raise RuntimeError(f'owned forward {port} exited; inspect {logpath}')
            if 'Forwarding from' in logpath.read_text()[offset:]:return
            time.sleep(0.5)
        raise RuntimeError(f'owned forward {port} not ready within 10s')
    def ro(self,path):
        if not self.token:raise RuntimeError('RECOVEROPS_TOKEN required')
        self.forward(18089,'svc/recoverops',8089)
        return http(18089,path,self.token)
    def connect(self, require_controller=True):
        save(self.out/'runtime.json',{'captured_at':utc(),'context':CTX,
             'deployments':json.loads(kubectl('get','deployments','-o','json')),
             'pods':json.loads(kubectl('get','pods','-o','json'))})
        for port,res,target,path in [(18080,'svc/labgateway',8080,'/healthz'),(19090,'svc/prometheus',9090,'/api/v1/alerts'),(18082,'deploy/labgateway',8082,'/admin/state')]:
            self.forward(port,res,target)
        if require_controller:self.wait(lambda:self.ro('/v1/limits'),60,'controller reachable')
        self.wait(lambda:http(19090,'/api/v1/alerts'),60,'Prometheus reachable')
        state=self.wait(lambda:http(18082,'/admin/state',self.admin),60,'gateway admin reachable')
        if state.get('faults'): raise RuntimeError('active gateway faults; refusing shared lab ownership')
        http(18082,'/admin/routing',self.admin,{'version':state['routingVersion'],'candidatePercent':0},'PUT')
    def capacity(self, required):
        raw=kubectl('exec','deploy/postgres','--','psql','-U','lab','-d','lab','-Atc',
                    "SELECT available FROM inventory WHERE sku='demo-item'")
        if int(raw.strip()) < required:
            raise RuntimeError(f'need at least {required} demo-item stock; see docs/CORRECTED_HANDOFF.md capacity procedure')
    def budget(self):
        def ready():
            x=self.ro('/v1/limits');return x if x['active']==0 and stamp(x['next_eligible_at'])<=time.time() else None
        self.wait(ready,3900,'policy cooldown/hourly budget')
    def clear(self):
        def inactive():
            alerts=http(19090,'/api/v1/alerts')['data']['alerts']
            return not any(a['labels'].get('alertname')=='LabBadTemplate' for a in alerts)
        self.wait(inactive,240,'prior alert cleared')
    def delay(self,seconds):
        obj=json.loads(kubectl('get','deployment','recoverops','-o','json'))
        c=obj['spec']['template']['spec']['containers'][0];args=c.get('args',[])
        if self.controller_original is None:self.controller_original=(obj['metadata']['uid'],list(args))
        elif obj['metadata']['uid']!=self.controller_original[0] or args!=self.controller_owned:
            raise RuntimeError('controller replaced or arguments changed by another operator')
        clean=[];skip=False
        for v in args:
            if skip: skip=False;continue
            if v=='--execution-delay-seconds':skip=True;continue
            if v.startswith('--execution-delay-seconds='):continue
            clean.append(v)
        clean+=['--execution-delay-seconds',str(seconds)]
        patch=[{'op':'test','path':'/metadata/resourceVersion','value':obj['metadata']['resourceVersion']},
               {'op':'replace','path':'/spec/template/spec/containers/0/args','value':clean}]
        kubectl('patch','deployment','recoverops','--type=json','-p',json.dumps(patch))
        self.controller_owned=clean
        kubectl('rollout','status','deployment/recoverops','--timeout=90s')
        self.wait(lambda:self.ro('/v1/limits'),60,'controller rollout')
    def arm(self, directory, seed, name):
        directory.mkdir(parents=True,exist_ok=True)
        record={'schema_version':3,'arm':name,'seed':seed,'started_at':utc(),'status':'RUNNING'}
        save(directory/'result.json',record)
        load=None;good=None;bad=None;restored=False
        try:
            self.budget();self.clear();self.delay(120 if name=='baseline' else 0)
            known={x['id'] for x in self.ro('/v1/incidents?limit=100')['incidents']}
            original=json.loads(kubectl('get','deployment','api-stable','-o','json'));save(directory/'before.json',original)
            good=original['spec']['template'];bad=json.loads(json.dumps(good))
            api=next(c for c in bad['spec']['containers'] if c['name']=='labapi')
            args=[a for a in api.get('args',[]) if not a.startswith(('-mode=','-error-rate='))]
            api['args']=args+['-mode=error','-error-rate=0.5']
            patch=[{'op':'test','path':'/metadata/uid','value':original['metadata']['uid']},
                   {'op':'test','path':'/metadata/resourceVersion','value':original['metadata']['resourceVersion']},
                   {'op':'replace','path':'/spec/template','value':bad}]
            kubectl('patch','deployment','api-stable','--type=json','-p',json.dumps(patch))
            record['fault_at']=utc();save(directory/'result.json',record)
            kubectl('rollout','status','deployment/api-stable','--timeout=90s')
            output=(directory/'load.log').open('w');self.files.append(output)
            prefix=hashlib.sha256(str(directory.resolve()).encode()).hexdigest()[:16]
            load=subprocess.Popen([str(ROOT/'bin/labload'),'run','--gateway','http://127.0.0.1:18080','--rate','15','--duration','420s',
                '--seed',str(seed),'--key-prefix',prefix,'--output',str(directory/'traffic.jsonl')],stdout=output,stderr=output)
            self.children.append(load)
            def occurrence():
                for x in self.ro('/v1/incidents?limit=100')['incidents']:
                    if x['id'] not in known: return x
            incident=self.wait(occurrence,240,'real Prometheus/Alertmanager incident')
            record['incident_id']=incident['id'];save(directory/'result.json',record)
            def terminal():
                x=self.ro('/v1/incidents/'+incident['id']);save(directory/'incident.json',x)
                return x if x['incident']['state'] in TERMINAL else None
            final=self.wait(terminal,330,'server measured recovery')
            record['status']=final['incident']['state']
            record['received_at']=final['incident']['created_at']
            record['executed_at']=final['intent']['executed_at']
            record['resolved_at']=final['incident']['updated_at']
            record['verification']=[e for e in final['events'] if e['kind']=='verified']
            if record['status']!='RESOLVED':raise RuntimeError('incident terminal without verified recovery')
            if name=='baseline' and stamp(record['executed_at'])-stamp(record['received_at'])<120:raise RuntimeError('baseline hold violated')
            after=json.loads(kubectl('get','deployment','api-stable','-o','json'));save(directory/'after.json',after)
            if after['metadata']['uid']!=original['metadata']['uid'] or after['spec']['template']!=good:raise RuntimeError('template/UID not restored')
            restored=True
            self.wait(lambda:load.poll() is not None,430,'fixed-duration workload completion')
            summary=json.loads((directory/'traffic.jsonl.summary.json').read_text());record['load_summary']=summary
            if load.returncode or not summary.get('Valid') or summary.get('Truncated'):raise RuntimeError('invalid workload')
            record['status']='VALID';record['verified_recovery_seconds']=stamp(record['resolved_at'])-stamp(record['received_at'])
        except BaseException as e:
            record['status']='INVALID';record['error']=str(e)
            raise
        finally:
            if load and load.poll() is None:
                load.terminate()
                try:load.wait(timeout=10)
                except subprocess.TimeoutExpired:record['workload_stop']='SIGTERM sent; exit unverified'
            # On failure, leave a durable halt and attempt only a conditional restore
            # of our own exact injected template. Never overwrite an operator edit.
            if good is not None and not restored:
                (self.out/'STOP').touch()
                try:
                    live=json.loads(kubectl('get','deployment','api-stable','-o','json'))
                    if live['metadata']['uid']==original['metadata']['uid'] and live['spec']['template']==bad:
                        patch=[{'op':'test','path':'/metadata/uid','value':live['metadata']['uid']}, {'op':'test','path':'/metadata/resourceVersion','value':live['metadata']['resourceVersion']}, {'op':'replace','path':'/spec/template','value':good}]
                        kubectl('patch','deployment','api-stable','--type=json','-p',json.dumps(patch));record['cleanup']='conditional restore submitted; readiness unverified'
                    else:record['cleanup']='target changed or already restored; no mutation'
                except Exception as e:record['cleanup_error']=str(e)
            record['finished_at']=utc();save(directory/'result.json',record)
        return record
    def pair(self,path,seed,order):
        path.mkdir(parents=True,exist_ok=True)
        results={};arms=['controller','baseline'] if order=='controller-first' else ['baseline','controller']
        for arm in arms:
            existing=path/arm/'result.json'
            if existing.exists():
                data=json.loads(existing.read_text())
                if data.get('schema_version')!=3 or data.get('seed')!=seed or data.get('status')!='VALID':
                    raise RuntimeError(f'preserved incomplete/invalid arm: {existing}; use new attempt directory after investigation')
                results[arm]=data;continue
            results[arm]=self.arm(path/arm,seed,arm)
            save(path/'pair.json',{'schema_version':3,'seed':seed,'order':order,'arms':results})
        return results
    def close(self):
        if self.controller_original is not None:
            try:
                obj=json.loads(kubectl('get','deployment','recoverops','-o','json'))
                uid,args=self.controller_original
                if obj['metadata']['uid']==uid and obj['spec']['template']['spec']['containers'][0].get('args',[])==self.controller_owned:
                    patch=[{'op':'test','path':'/metadata/uid','value':uid},{'op':'test','path':'/metadata/resourceVersion','value':obj['metadata']['resourceVersion']},{'op':'replace','path':'/spec/template/spec/containers/0/args','value':args}]
                    kubectl('patch','deployment','recoverops','--type=json','-p',json.dumps(patch))
                    kubectl('rollout','status','deployment/recoverops','--timeout=90s')
                    save(self.out/'controller-cleanup.json',{'status':'restored original arguments'})
                else:
                    save(self.out/'controller-cleanup.json',{'status':'CONFLICT: operator change preserved'})
                    (self.out/'STOP').touch()
            except Exception as err:
                save(self.out/'controller-cleanup.json',{'status':'UNVERIFIED','error':str(err)})
                (self.out/'STOP').touch()
        for p in reversed(self.children):
            if p.poll() is None:
                p.terminate()
                try:p.wait(timeout=10)
                except subprocess.TimeoutExpired: pass
        for f in self.files:f.close()

def main():
    ap=argparse.ArgumentParser();ap.add_argument('mode',choices=['pair','matrix','demo'])
    ap.add_argument('--out',type=pathlib.Path,required=True);ap.add_argument('--seed',type=int,default=500)
    ap.add_argument('--order',choices=['controller-first','baseline-first'],default='controller-first')
    args=ap.parse_args();os.chdir(ROOT);args.out=args.out.resolve();args.out.mkdir(parents=True,exist_ok=True)
    (ROOT/'results').mkdir(exist_ok=True)
    lock=(ROOT/'results/.recoverops-experiment.lock').open('a');fcntl.flock(lock,fcntl.LOCK_EX|fcntl.LOCK_NB)
    command(['./scripts/require-context.sh']);floor(args.out)
    manifest=args.out/'manifest.json'
    if manifest.exists():
        previous=json.loads(manifest.read_text())
        if previous.get('commit')!=command(['git','rev-parse','HEAD']).strip() or previous.get('seed')!=args.seed or previous.get('mode')!=args.mode or previous.get('order')!=args.order:
            raise RuntimeError('resume manifest differs; use a new evidence directory')
    if command(['git','status','--porcelain','--untracked-files=no']).strip():raise RuntimeError('commit tracked source changes before benchmark')
    if not manifest.exists():save(manifest,{'schema_version':3,'created_at':utc(),'seed':args.seed,'mode':args.mode,'order':args.order,'commit':command(['git','rev-parse','HEAD']).strip(),'versions':json.loads((ROOT/'versions.lock.json').read_text()),'policy_sha256':hashlib.sha256((ROOT/'configs/policies/lab-rollback.yaml').read_bytes()).hexdigest(),'environment':'kind-sre-lab','baseline':'measured 120s delay from durable incident receipt; not human-response evidence'})
    driver=Driver(args.out)
    def stop(*_):raise KeyboardInterrupt('interrupted; evidence preserved')
    signal.signal(signal.SIGTERM,stop)
    try:
        driver.connect()
        driver.capacity(150000 if args.mode=='matrix' else 15000)
        if args.mode=='pair':driver.pair(args.out,args.seed,args.order)
        elif args.mode=='demo':driver.arm(args.out/'controller',args.seed,'controller')
        else:
            driver.pair(args.out/'pilot',args.seed,'controller-first')
            for i in range(10):
                driver.pair(args.out/f'pair-{i+1:02}',args.seed+i+1,'controller-first' if i%2==0 else 'baseline-first')
                command([sys.executable,'scripts/recoverops-report.py',str(args.out)])
    finally:driver.close();lock.close()
    if (args.out/'STOP').exists():raise RuntimeError('cleanup incomplete; see STOP and controller-cleanup.json')
    print('COMPLETE',args.out,flush=True)
if __name__=='__main__':main()
