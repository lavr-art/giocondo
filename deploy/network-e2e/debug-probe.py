# Diagnostic helper: writes a private client config, never prints credentials.
import json,sqlite3,pathlib
conn=sqlite3.connect('/work/panel.db')
u=json.loads(conn.execute('select credentials from node_monitors limit 1').fetchone()[0])
p=json.loads(conn.execute("select params from inbounds where type='hysteria2' limit 1").fetchone()[0])
cfg={'log':{'level':'debug'},'inbounds':[{'type':'http','listen':'127.0.0.1','listen_port':19999}], 'dns':{'servers':[{'type':'local','tag':'local'}]},'route':{'final':'probe','default_domain_resolver':'local'},'outbounds':[{'type':'hysteria2','tag':'probe','server':'172.30.77.11','server_port':8443,'password':u['uuid'],'obfs':{'type':'salamander','password':p['obfs_password']},'tls':{'enabled':True,'server_name':'n1.vpn.test','certificate':[p['cert_pem']]}}]}
path=pathlib.Path('/work/debug.json');path.write_text(json.dumps(cfg));path.chmod(0o600)
