"""One-line installer on a clean server: install, real client traffic, then an idempotent second run.

Run with: sh test/network-e2e/run.sh installer
"""
import json
from test import exec_,wait,WORK,REPORT

IP='172.30.77.31'
CHALLENGE='b'*64
CHALLENGE_URL='https://192.0.2.200:8443/installer-challenge.txt'
INSTALL=f'curl -fsSL http://control:8000/repo/install.sh -o /root/install.sh && sh /root/install.sh --ip {IP} --http --repo http://control:8000/repo --yes'
STATE_FILES=['/etc/giocondo-vpn/panel.env','/etc/giocondo-vpn/agent/agent.crt','/var/lib/giocondo-vpn/panel/pki/server.crt']
SERVICES=['giocondo-vpn-panel','giocondo-vpn-agent','giocondo-vpn-sing-box']

def sh(cmd,check=True):return exec_('installer','sh','-c',cmd,check=check)
def api(method,path,body=None):
 data=f"-d '{json.dumps(body)}'" if body is not None else ''
 out=sh(f'. /etc/giocondo-vpn/panel.env && curl -fsS -X {method} -H "Authorization: Bearer $(cat /root/token)" '
        f'-H "Content-Type: application/json" {data} "http://127.0.0.1:8080$PANEL_ADMIN_PATH/api/{path}"').stdout
 return json.loads(out)
def snapshot():
 files=sh('sha256sum '+' '.join(STATE_FILES)).stdout
 started=sh('systemctl show -p ActiveEnterTimestamp '+' '.join(SERVICES)).stdout
 return files+started
def client_request(port):
 p=exec_('client','curl','-fsS','--max-time','8','--cacert','/work/pki/ca.crt','-x',f'http://127.0.0.1:{port}',CHALLENGE_URL,check=False)
 return p.returncode==0 and p.stdout==CHALLENGE

def main():
 # A static control fixture avoids depending on the panel container or a private destination.
 (WORK/'installer-challenge.txt').write_text(CHALLENGE)
 wait('systemd ready in the clean server',lambda:sh('systemctl is-system-running --wait',check=False).returncode in (0,1))
 out=sh(INSTALL).stdout;print(out,flush=True)
 assert 'giocondo-vpn is installed.' in out,out
 wait('all services active after install',lambda:all(sh(f'systemctl is-active {s}',check=False).stdout.strip()=='active' for s in SERVICES),120)
 sh('runuser -u giocondo-vpn -- giocondo-vpn-panel token create -name e2e -scope admin -db /var/lib/giocondo-vpn/panel/panel.db | tail -n 1 > /root/token && chmod 600 /root/token')
 def node_applied():
  n=api('GET','nodes')
  return len(n)==1 and n[0]['status']=='online' and not n[0]['dirty'] and not n[0]['apply_error']
 wait('local node online with the config applied',node_applied,180)
 user=api('POST','users',{'name':'bob','days':30})
 token=user['subscription_url'].rsplit('/',1)[-1]
 assert user['subscription_url']==f'http://{IP}:8081/sub/{token}',user['subscription_url']
 def subscription():
  script=f"import urllib.request;print(urllib.request.urlopen('http://{IP}:8081/sub/{token}?format=singbox').read().decode())"
  return json.loads(exec_('client','python3','-c',script).stdout)
 profile=wait('subscription served on the public IP',subscription,60)
 selected={o['type']:o for o in profile['outbounds'] if o['type'] in ('vless','hysteria2')}
 assert set(selected)=={'vless','hysteria2'},profile['outbounds']
 for index,(kind,out) in enumerate(sorted(selected.items()),1):
  out['tag']='vpn';port=19100+index
  cfg={'log':{'level':'error'},'inbounds':[{'type':'http','listen':'127.0.0.1','listen_port':port}],
       'dns':{'servers':[{'type':'udp','tag':'dns','server':'172.30.77.3'}],'strategy':'ipv4_only'},
       'route':{'final':'vpn','default_domain_resolver':'dns'},'outbounds':[out]}
  path=WORK/f'client-installer-{kind}.json';path.write_text(json.dumps(cfg));path.chmod(0o600)
  exec_('client','sh','-c',f'nohup /work/sing-box run -c /work/client-installer-{kind}.json > /work/client-installer-{kind}.log 2>&1 &')
  wait(f'client traffic through the installed node: {kind}',lambda port=port:client_request(port),90)
 before=snapshot()
 out=sh(INSTALL).stdout
 assert 'giocondo-vpn is installed.' in out,out
 assert before==snapshot(),'second run changed state:\n'+before+'\n---\n'+snapshot()
 print('PASS second run changes no files and restarts no services',flush=True);REPORT.append('installer is idempotent')
 healthy_files=sh('sha256sum '+' '.join(STATE_FILES)).stdout
 other_services=sh('systemctl show -p ActiveEnterTimestamp giocondo-vpn-panel giocondo-vpn-sing-box').stdout
 sh('systemctl stop giocondo-vpn-agent')
 out=sh(INSTALL).stdout
 assert 'giocondo-vpn is installed.' in out,out
 assert healthy_files==sh('sha256sum '+' '.join(STATE_FILES)).stdout,'recovery replaced credentials'
 assert other_services==sh('systemctl show -p ActiveEnterTimestamp giocondo-vpn-panel giocondo-vpn-sing-box').stdout,'recovery restarted healthy services'
 assert node_applied(),'restarted agent is not ready'
 print('PASS stopped agent recovered without replacing credentials or restarting healthy services',flush=True)
 REPORT.append('stopped agent recovered safely')
 status=sh('sh /root/install.sh status').stdout
 states=dict(line.split()[:2] for line in status.splitlines() if line.startswith('giocondo-vpn-'))
 assert states=={s:'active' for s in SERVICES},status
 print('PASS status reports active services',flush=True)
 sh('apt-get update -qq && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq caddy >/dev/null')
 print(exec_('installer','python3','/src/test/network-e2e/caddy-check.py').stdout,flush=True)
 REPORT.append("generated domain and ip Caddy HTTPS proxies with verified local TLS")
 (WORK/'installer-results.txt').write_text('\n'.join(REPORT)+'\n')

if __name__=='__main__':main()
