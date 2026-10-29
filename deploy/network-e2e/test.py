"""Host runner: all network actions take place inside the isolated Compose project."""
import base64
import hashlib
import json
import os
import pathlib
import subprocess
import time

ROOT=pathlib.Path(__file__).resolve().parents[2]
WORK=ROOT/'bin/network-e2e'
COMPOSE=['docker','compose','-f',str(ROOT/'deploy/network-e2e/compose.yaml')]
REPORT=[]
def run(*args,check=True):
 result=subprocess.run(COMPOSE+list(args),check=False,text=True,capture_output=True)
 if check and result.returncode:raise RuntimeError(result.stderr or result.stdout)
 return result
def exec_(service,*args,check=True):return run('exec','-T',service,*args,check=check)
def api(method,path,body=None):
 args=['python3','/src/deploy/network-e2e/request.py',method,path]
 if body is not None:args.append(json.dumps(body))
 result=exec_('panel',*args)
 return json.loads(result.stdout)
def wait(label,fn,timeout=180):
 start=time.monotonic();last=None
 while time.monotonic()-start<timeout:
  try:
   last=fn()
   if last:print('PASS',label,flush=True);REPORT.append(label);return last
  except (subprocess.CalledProcessError,json.JSONDecodeError,RuntimeError) as e:last=str(e)
  time.sleep(2)
 raise AssertionError(f'{label}: timeout; last={last}')
def job(op):
 def poll():
  value=api('GET','jobs/'+op['id'])
  if value['status']=='failed':raise AssertionError(value)
  return value if value['status']=='ok' else None
 return wait('operation '+op['id'][:8],poll,300)
def node():return api('GET','nodes/'+str(NODE))
def healthy():
 n=node();ps=[p for p in n['probes'] if p['ip']==n['ip']]
 return n['status']=='online' and not n['dirty'] and len(ps)>=6 and all(not p['error'] for p in ps)
def client_request(kind):
 port={'vless':19001,'hy2':19002,'wg':19003}[kind]
 p=exec_('client','curl','-fsS','--max-time','8','--cacert','/work/pki/ca.crt','-x',f'http://127.0.0.1:{port}','https://192.0.2.200:8443/health/challenge?challenge='+'a'*64,check=False)
 return p.returncode==0 and p.stdout=='a'*64

def main():
 global NODE
 wait('panel API',lambda:api('GET','nodes') is not None)
 existing=api('GET','nodes')
 if existing:
  if os.environ.get('NETWORK_E2E_RESUME')!='1':raise AssertionError('Use a fresh test DB/work directory; prior reports are preserved')
  NODE=existing[0]['id'];user=api('GET','users')[0];preview=api('GET',f'nodes/{NODE}/dns')
 else:
  created=api('POST','nodes',{'name':'n1','host':'172.30.77.11','ip':'172.30.77.11','vless_reality':{'dest':'192.0.2.200:8443','server_name':'control'},'hysteria2':{'obfs':True},'wireguard':{}});NODE=created['id']
  user=api('POST','users',{'name':'alice','days':30})
  api('POST',f'nodes/{NODE}/addresses',{'ip':'172.30.77.12'})
  preview=api('GET',f'nodes/{NODE}/dns');assert preview['host']==f'n{NODE}.vpn.test'
  api('POST',f'nodes/{NODE}/dns',{'confirm':True})
 job(api('POST',f'nodes/{NODE}/provision-ssh',{'host':'172.30.77.11','password':'network-test-only','keep_key':True}))
 wait('all three protocols healthy from panel and external probe',healthy,420)
 wait('reserve IP supports UDP from both points',lambda:len([p for p in node()['probes'] if p['ip']=='172.30.77.12' and not p['error']])==6,420)
 # Capture the subscription exactly once; clients keep the same config and process.
 script="import urllib.request;print(urllib.request.urlopen('http://panel:8081/sub/"+user['subscription_url'].rsplit('/',1)[-1]+"?format=singbox').read().decode())"
 profile=json.loads(exec_('client','python3','-c',script).stdout)
 selected={}
 for out in profile['outbounds']:
  if out['type']=='vless':selected['vless']=out
  if out['type']=='hysteria2':selected['hy2']=out
 selected['wg']=profile['endpoints'][0]
 for index,(kind,out) in enumerate(selected.items(),1):
  assert out.get('server',out.get('peers',[{}])[0].get('address'))==preview['host']
  out['tag']='vpn'
  cfg={'log':{'level':'error'},'inbounds':[{'type':'http','listen':'127.0.0.1','listen_port':19000+index}],
       'dns':{'servers':[{'type':'udp','tag':'dns','server':'172.30.77.3'}],'strategy':'ipv4_only'},
       'route':{'final':'vpn','default_domain_resolver':'dns'}}
  cfg['endpoints' if kind=='wg' else 'outbounds']=[out]
  path=WORK/f'client-{kind}.json';path.write_text(json.dumps(cfg));path.chmod(0o600)
  exec_('client','sh','-c',f'nohup /work/sing-box run -c /work/client-{kind}.json > /work/client-{kind}.log 2>&1 & echo $! > /work/client-{kind}.pid')
 fingerprints={p.name:hashlib.sha256(p.read_bytes()).hexdigest() for p in WORK.glob('client-*.json')}
 for kind in selected:wait('initial client traffic '+kind,lambda kind=kind:client_request(kind),60)
 before=exec_('node1','cat','/etc/vpnctl/sing-box/config.json').stdout
 # Drop public listeners only on IP .11; SSH/agent control traffic remain intact.
 for protocol,ports in [('tcp','443'),('udp','8443,51820')]:
  exec_('node1','iptables','-t','raw','-I','PREROUTING','1','-d','172.30.77.11','-p',protocol,'-m','multiport','--dports',ports,'-j','DROP')
 wait('warning after three failures with live agent',lambda:node()['warning'] and node()['agent_connected'],1200 if os.environ.get('PANEL_HEALTH_INTERVAL')=='5m' else 300)
 blocked=node();assert all(p['failures']==3 for p in blocked['probes'] if p['ip']=='172.30.77.11')
 assert not client_request('hy2') and not client_request('wg')
 for kind in selected:
  pid=exec_('client','cat',f'/work/client-{kind}.pid').stdout.strip();exec_('client','kill','-0',pid)
 started=time.monotonic()
 job(api('POST',f'nodes/{NODE}/rotate-ip',{'mode':'existing_ip','ip':'172.30.77.12'}))
 for kind in selected:wait('same client process reconnects after DNS switch '+kind,lambda kind=kind:client_request(kind),150)
 REPORT.append(f'DNS reconnection elapsed {time.monotonic()-started:.1f}s (TTL 60)')
 assert exec_('node1','cat','/etc/vpnctl/sing-box/config.json').stdout==before
 wait('warning cleared on new IP',lambda:not node()['warning'])
 assert {p.name:hashlib.sha256(p.read_bytes()).hexdigest() for p in WORK.glob('client-*.json')}==fingerprints
 # A fresh server receives exactly the same logical configuration and keys.
 op=api('POST',f'nodes/{NODE}/rotate-ip',{'mode':'ssh','ip':'172.30.77.21','ssh':{'password':'network-test-only','keep_key':True},'source_ssh':{'host':'172.30.77.12','password':'network-test-only'}})
 job(op)
 for kind in selected:wait('same client config after SSH migration '+kind,lambda kind=kind:client_request(kind),150)
 assert exec_('node2','cat','/etc/vpnctl/sing-box/config.json').stdout==before
 assert node()['host']==preview['host'] and node()['ip']=='172.30.77.21'
 assert exec_('node1','systemctl','is-active','vpnctl-agent',check=False).returncode!=0
 REPORT.append('SSH migration preserved complete config, keys, node ID and subscription')
 # A failed readiness check must not change DNS or the active address.
 old=api('GET',f'nodes/{NODE}/addresses')
 api('POST',f'nodes/{NODE}/addresses',{'ip':'172.30.77.99'})
 failed=api('POST',f'nodes/{NODE}/rotate-ip',{'mode':'existing_ip','ip':'172.30.77.99'})
 wait('unreachable candidate fails without DNS switch',lambda:api('GET','jobs/'+failed['id'])['status']=='failed',90)
 assert node()['ip']=='172.30.77.21'
 REPORT.append('unreachable candidate leaves active IP unchanged')
 output='\n'.join(REPORT)+'\n'
 (WORK/'results.txt').write_text(output);print(output,flush=True)

if __name__=='__main__':main()
