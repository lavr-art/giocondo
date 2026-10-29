"""Acceptance at the production 5-minute interval, after the fast E2E suite.

Run after a successful run.sh (it expects the post-migration state: one node on .21), ~13 minutes:
  PANEL_HEALTH_INTERVAL=5m python3 test/network-e2e/cadence.py
"""
import datetime
import json
import pathlib
import subprocess
import time
from test import api,exec_,run,WORK,REPORT,wait

nodes=api('GET','nodes');assert len(nodes)==1
n=nodes[0];nid=n['id'];assert n['ip']=='172.30.77.21'
for a in n['addresses']:
 if a['ip']=='172.30.77.99':api('DELETE',f'nodes/{nid}/addresses/'+str(a['id']))
# A healthy fresh round clears earlier transient failures before timing the test.
wait('healthy active node before cadence check',lambda:all(not p['error'] for p in api('GET',f'nodes/{nid}')['probes'] if p['ip']==n['ip']),180)
exec_('node2','iptables','-t','raw','-I','PREROUTING','1','-d',n['ip'],'-p','tcp','--dport','443','-j','DROP')
# First check runs at startup, then twice on the unchanged production 5m ticker.
run('up','-d','--force-recreate','panel')
observations=[];deadline=time.monotonic()+780
while time.monotonic()<deadline:
 try:
  current=api('GET',f'nodes/{nid}')
  p=next((p for p in current['probes'] if p['ip']==n['ip'] and p['tag']=='vless' and p['point']=='panel'),None)
  if p and p['failures'] and (not observations or observations[-1]['failures']!=p['failures']):
   observations.append(p);print('CADENCE',p['failures'],p['checked_at'],flush=True)
   if p['failures']<3:assert not current['warning'],'early warning'
  if current['warning'] and observations and observations[-1]['failures']==3:break
 except (RuntimeError,StopIteration):pass
 time.sleep(5)
else:raise AssertionError('5-minute cadence test timed out')
assert [p['failures'] for p in observations]==[1,2,3],observations
stamps=[datetime.datetime.fromisoformat(p['checked_at'].replace('Z','+00:00')) for p in observations]
intervals=[(b-a).total_seconds() for a,b in zip(stamps,stamps[1:])]
assert all(285<=d<=315 for d in intervals),intervals
print('PASS production 5-minute intervals',intervals,flush=True)
(WORK/'cadence-results.json').write_text(json.dumps({'intervals_seconds':intervals,'observations':observations},indent=2))
exec_('node2','iptables','-t','raw','-D','PREROUTING','-d',n['ip'],'-p','tcp','--dport','443','-j','DROP')
