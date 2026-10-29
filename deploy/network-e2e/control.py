"""Local Cloudflare API, authoritative test DNS, TLS reverse proxy and apt server."""
import http.server
import json
import pathlib
import socket
import socketserver
import ssl
import struct
import threading
import urllib.parse
import urllib.request

ROOT=pathlib.Path('/work')
STATE=ROOT/'records.json'
lock=threading.Lock()
records=json.loads(STATE.read_text()) if STATE.exists() else []

class HTTP(http.server.SimpleHTTPRequestHandler):
 def __init__(self,*args,**kwargs):super().__init__(*args,directory=str(ROOT),**kwargs)
 def log_message(self,*args):pass
 def reply(self,data,code=200):
  self.send_response(code);self.send_header('Content-Type','application/json');self.end_headers();self.wfile.write(json.dumps(data).encode())
 def do_GET(self):
  u=urllib.parse.urlsplit(self.path)
  if u.path.startswith('/zones/test/dns_records'):
   if self.headers.get('Authorization')!='Bearer local-test-token':return self.reply({},403)
   name=urllib.parse.parse_qs(u.query).get('name',[''])[0]
   with lock:self.reply({'success':True,'result':[r for r in records if not name or r['name']==name]})
  elif u.path=='/events':
   p=ROOT/'events.jsonl';self.reply([json.loads(s) for s in p.read_text().splitlines()] if p.exists() else [])
  elif u.path=='/health/challenge':
   with urllib.request.urlopen('http://panel:8081'+self.path) as r:
    self.send_response(r.status);self.end_headers();self.wfile.write(r.read())
  else:super().do_GET()
 def do_POST(self):
  data=json.loads(self.rfile.read(int(self.headers.get('Content-Length',0))))
  if self.path=='/events':
   with lock:
    with (ROOT/'events.jsonl').open('a') as f:f.write(json.dumps(data)+'\n')
   return self.reply({})
  if self.headers.get('Authorization')!='Bearer local-test-token':return self.reply({},403)
  with lock:
   data['id']='record-'+str(len(records)+1);records.append(data);STATE.write_text(json.dumps(records));self.reply({'success':True,'result':data})
 def do_PATCH(self):
  if self.headers.get('Authorization')!='Bearer local-test-token':return self.reply({},403)
  data=json.loads(self.rfile.read(int(self.headers.get('Content-Length',0))));rid=self.path.rsplit('/',1)[-1]
  with lock:
   for i,r in enumerate(records):
    if r['id']==rid:
     data['id']=rid;records[i]=data;STATE.write_text(json.dumps(records));return self.reply({'success':True,'result':data})
  self.reply({'success':False},404)

# Minimal authoritative A/AAAA DNS; answers carry the configured TTL.
def answer(raw):
 idx=12;labels=[]
 while raw[idx]:
  size=raw[idx];idx+=1;labels.append(raw[idx:idx+size].decode());idx+=size
 idx+=1;qtype,qclass=struct.unpack('!HH',raw[idx:idx+4]);question=raw[12:idx+4];name='.'.join(labels)
 with lock:found=[r for r in records if r['name']==name and qtype==1]
 header=raw[:2]+struct.pack('!HHHHH',0x8580,1,len(found),0,0)
 return header+question+b''.join(b'\xc0\x0c'+struct.pack('!HHIH',1,1,r['ttl'],4)+socket.inet_aton(r['content']) for r in found)
class UDP(socketserver.BaseRequestHandler):
 def handle(self):raw,s=self.request;s.sendto(answer(raw),self.client_address)
class TCP(socketserver.BaseRequestHandler):
 def handle(self):
  size=struct.unpack('!H',self.request.recv(2))[0];raw=b''
  while len(raw)<size:raw+=self.request.recv(size-len(raw))
  result=answer(raw);self.request.sendall(struct.pack('!H',len(result))+result)
for server in [socketserver.ThreadingUDPServer(('0.0.0.0',53),UDP),socketserver.ThreadingTCPServer(('0.0.0.0',53),TCP)]:threading.Thread(target=server.serve_forever,daemon=True).start()
plain=http.server.ThreadingHTTPServer(('0.0.0.0',8000),HTTP)
threading.Thread(target=plain.serve_forever,daemon=True).start()
tls=http.server.ThreadingHTTPServer(('0.0.0.0',8443),HTTP)
ctx=ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER);ctx.load_cert_chain('/work/pki/server.crt','/work/pki/server.key');tls.socket=ctx.wrap_socket(tls.socket,server_side=True);tls.serve_forever()
