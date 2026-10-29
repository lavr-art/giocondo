import json,pathlib,sys,urllib.request,urllib.error
method,path=sys.argv[1:3]
body=sys.argv[3].encode() if len(sys.argv)>3 else None
token=pathlib.Path('/work/token-output').read_text().splitlines()[-1]
req=urllib.request.Request('http://127.0.0.1:8080/test-admin/api/'+path,data=body,method=method,headers={'Authorization':'Bearer '+token,'Content-Type':'application/json'})
try:
 with urllib.request.urlopen(req,timeout=25) as r:
  data=r.read();sys.stdout.buffer.write(data if data else b'{}')
except urllib.error.HTTPError as e:
 print(e.code,e.read().decode(),file=sys.stderr);sys.exit(1)
