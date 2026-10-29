#!/bin/sh
set -eu
[ -x /work/panel ] && [ -x /work/agent ] && [ -x /work/sing-box ]
mkdir -p /work/packages
python3 - <<'PY'
import os, pathlib, shutil, subprocess
root=pathlib.Path('/work/packages')
arch=subprocess.check_output(['dpkg','--print-architecture'],text=True).strip()
for pkg in ['agent','sing-box']:
 d=root/pkg
 if d.exists(): shutil.rmtree(d)
 (d/'DEBIAN').mkdir(parents=True)
 depends='Depends: giocondo-vpn-sing-box\n' if pkg=='agent' else ''
 (d/'DEBIAN/control').write_text(f'Package: giocondo-vpn-{pkg}\nVersion: 0.0.1~network7\nArchitecture: {arch}\nMaintainer: local test <test@example.invalid>\n{depends}Description: Local task 7 integration package\n')
 files=[(f'/work/{pkg}', 'usr/bin/giocondo-vpn-agent' if pkg=='agent' else 'usr/lib/giocondo-vpn/sing-box'),(f'/src/deploy/apt/systemd/giocondo-vpn-{pkg}.service',f'lib/systemd/system/giocondo-vpn-{pkg}.service')]
 if pkg=='agent': files.append(('/src/deploy/apt/deb/agent.env','usr/share/giocondo-vpn/agent.env.example'))
 for source,dest in files:
  p=d/dest;p.parent.mkdir(parents=True,exist_ok=True);shutil.copy2(source,p)
 shutil.copy2(f'/src/deploy/apt/deb/{pkg}-postinst.sh',d/'DEBIAN/postinst');os.chmod(d/'DEBIAN/postinst',0o755)
 subprocess.run(['dpkg-deb','--build',str(d),f'/work/packages/giocondo-vpn-{pkg}_0.0.1~network7_{arch}.deb'],check=True,stdout=subprocess.DEVNULL)
PY
# gpg-agent cannot keep its sockets on the host bind mount (/work); with /run/user/0 present GnuPG puts them there.
mkdir -p -m 700 /run/user/0
if [ ! -d /work/gnupg ]; then
 mkdir -m 700 /work/gnupg
 GNUPGHOME=/work/gnupg gpg --batch --passphrase '' --quick-gen-key 'Network test <network@example.invalid>' ed25519 sign never
fi
export GNUPGHOME=/work/gnupg
KEY=$(gpg --list-secret-keys --with-colons | awk -F: '/^fpr/ {print $10; exit}')
/src/deploy/apt/update-repo.sh /work/repo "$KEY" /work/packages/*_0.0.1~network7_*.deb
if [ ! -f /work/pki/ca.key ]; then /work/panel ca init -dir /work/pki; fi
/work/panel ca server -dir /work/pki -host panel,control,probe,192.0.2.200,172.30.77.2,172.30.77.3,172.30.77.5
printf 'setup complete\n'
