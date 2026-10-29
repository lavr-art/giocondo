import socket,struct,time
s=socket.socket(socket.AF_PACKET,socket.SOCK_RAW,socket.htons(3));s.settimeout(1);end=time.monotonic()+12
while time.monotonic()<end:
 try:b=s.recv(2048)
 except TimeoutError:continue
 if len(b)<42 or b[12:14]!=bytes.fromhex("0800") or b[23]!=17:continue
 src=socket.inet_ntoa(b[26:30]);dst=socket.inet_ntoa(b[30:34]);offset=14+(b[14]&15)*4;sp,dp=struct.unpack('!HH',b[offset:offset+4])
 if sp in (8443,51820) or dp in (8443,51820):print(src,sp,'->',dst,dp,flush=True)
