#!/bin/sh
set -eu
# Use the exact GoReleaser artifacts under test, never synthetic packages.
python3 /src/test/network-e2e/unpack-packages.py
# A prior candidate must never win APT's version selection in a subsequent run.
python3 - <<'PYCLEAN'
import shutil
for name in ('repo', 'baseline-repo'):
 shutil.rmtree('/work/' + name, ignore_errors=True)
PYCLEAN
# gpg-agent cannot keep its sockets on the host bind mount (/work); with /run/user/0 present GnuPG puts them there.
mkdir -p -m 700 /run/user/0
if [ ! -d /work/gnupg ]; then
 mkdir -m 700 /work/gnupg
 GNUPGHOME=/work/gnupg gpg --batch --passphrase '' --quick-gen-key 'Network test <network@example.invalid>' ed25519 sign never
fi
export GNUPGHOME=/work/gnupg
KEY=$(gpg --list-secret-keys --with-colons | awk -F: '/^fpr/ {print $10; exit}')
/src/deploy/apt/update-repo.sh /work/repo "$KEY" /work/candidate/*.deb
cp /src/install.sh /work/repo/install.sh
if [ -d /work/baseline ]; then
 /src/deploy/apt/update-repo.sh /work/baseline-repo "$KEY" /work/baseline/*.deb
 cp /src/install.sh /work/baseline-repo/install.sh
fi
if [ ! -f /work/pki/ca.key ]; then /work/panel ca init -dir /work/pki; fi
/work/panel ca server -dir /work/pki -host panel,control,probe,192.0.2.200,172.30.77.2,172.30.77.3,172.30.77.5
printf 'setup complete\n'
