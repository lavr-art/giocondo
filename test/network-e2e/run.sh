#!/bin/sh
set -eu
cd "$(dirname "$0")/../.."
compose() { docker compose -f test/network-e2e/compose.yaml "$@"; }
case "${1:-test}" in
 down) compose down --remove-orphans; exit;;
 logs) compose logs --tail=100; exit;;
esac
ARCH=$(docker info --format '{{.Architecture}}')
case "$ARCH" in aarch64|arm64) ARCH=arm64;;x86_64|amd64) ARCH=amd64;;*) echo 'Unsupported Docker architecture' >&2;exit 1;;esac
mkdir -p bin/network-e2e
# Build with `make deb` first, or point CANDIDATE_DEBS at downloaded candidate artifacts.
python3 test/network-e2e/prepare-packages.py "$ARCH" "${1:-test}"
if [ "${1:-}" = fresh ]; then
 compose down --remove-orphans
 python3 - <<'PYRESET'
import pathlib,time,shutil
root=pathlib.Path('bin/network-e2e');out=root/'archive'/str(time.time_ns());out.mkdir(parents=True)
for pattern in ['panel.db*','records.json','events.jsonl','token-output','results.txt','client-*','debug*']:
 for p in root.glob(pattern):shutil.move(str(p),str(out/p.name))
PYRESET
fi
compose build setup
compose run --rm setup
if [ "${1:-}" = installer ] || [ "${1:-}" = upgrade ]; then
 # Clean server and clients: a prior run must not leave processes with old credentials.
 compose rm -sf installer client
 compose up -d control installer client
 compose exec -T control ip addr replace 192.0.2.200/32 dev eth0
 for service in installer client; do
  compose exec -T "$service" ip route replace 192.0.2.200/32 via 172.30.77.3
 done
 if [ "${1:-}" = upgrade ]; then
  python3 test/network-e2e/upgrade.py
 else
  python3 test/network-e2e/installer.py
 fi
 exit
fi
compose up -d control panel node1 node2 client probe
compose exec -T node1 ip addr add 172.30.77.12/24 dev eth0 2>/dev/null || compose exec -T node1 ip addr show dev eth0 | grep -q '172.30.77.12'
compose exec -T control ip addr add 192.0.2.200/32 dev eth0
for service in panel probe client node1 node2; do
 compose exec -T "$service" ip route replace 192.0.2.200/32 via 172.30.77.3
 done
python3 test/network-e2e/test.py
