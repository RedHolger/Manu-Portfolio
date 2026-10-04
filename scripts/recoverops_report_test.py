import importlib.util,json,pathlib,tempfile,unittest
spec=importlib.util.spec_from_file_location('report',pathlib.Path(__file__).with_name('recoverops-report.py'))
r=importlib.util.module_from_spec(spec);spec.loader.exec_module(r)
class ReportTest(unittest.TestCase):
 def test_incomplete_and_mismatched_preserved(self):
  with tempfile.TemporaryDirectory() as t:
   p=pathlib.Path(t)/'pair-01/controller';p.mkdir(parents=True)
   a={'schema_version':3,'seed':4,'status':'VALID','verification':[{}],'verified_recovery_seconds':30}
   (p/'result.json').write_text(json.dumps(a))
   rows,_,n,pilot=r.summarize(pathlib.Path(t));self.assertEqual(n,0);self.assertEqual(rows[0][1]['baseline']['status'],'MISSING')
   b=p.parent/'baseline';b.mkdir();a['seed']=5;(b/'result.json').write_text(json.dumps(a))
   self.assertEqual(r.summarize(pathlib.Path(t))[2],0)
   a['seed']=4;a['verified_recovery_seconds']=150;(b/'result.json').write_text(json.dumps(a))
   self.assertEqual(r.summarize(pathlib.Path(t))[1],[120])
   a['status']='INVALID';(b/'result.json').write_text(json.dumps(a))
   self.assertEqual(r.summarize(pathlib.Path(t))[2],0)
 def test_old_schema_and_nonfinite_excluded(self):
  for bad in [{'schema_version':2},{'verified_recovery_seconds':float('nan')}]:
   a={'schema_version':3,'seed':4,'status':'VALID','verification':[{}],'verified_recovery_seconds':30}
   self.assertFalse(r.qualifies({'controller':a,'baseline':dict(a,**bad)}))
if __name__=='__main__':unittest.main()
