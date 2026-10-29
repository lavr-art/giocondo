#!/bin/sh
set -eu
cd "$(dirname "$0")/../.."
compose() { docker compose -f deploy/network-e2e/compose.yaml "$@"; }
case "${1:-test}" in
 down) compose down --remove-orphans; exit;;
 logs) compose logs --tail=100; exit;;
esac
ARCH=$(docker info --format '{{.Architecture}}')
case "$ARCH" in aarch64|arm64) ARCH=arm64;;x86_64|amd64) ARCH=amd64;;*) echo 'Unsupported Docker architecture' >&2;exit 1;;esac
mkdir -p bin/network-e2e
GOOS=linux GOARCH="$ARCH" go build -o bin/network-e2e/panel ./cmd/panel
GOOS=linux GOARCH="$ARCH" go build -o bin/network-e2e/agent ./cmd/agent
if [ -n "${SINGBOX_BINARY:-}" ]; then
 cp "$SINGBOX_BINARY" bin/network-e2e/sing-box
elif [ ! -x bin/network-e2e/sing-box ]; then
 make sing-box GOOS=linux GOARCH="$ARCH"
 cp "bin/sing-box-linux-$ARCH" bin/network-e2e/sing-box
fi
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
compose up -d control panel node1 node2 client probe
compose exec -T node1 ip addr add 172.30.77.12/24 dev eth0 2>/dev/null || compose exec -T node1 ip addr show dev eth0 | rg -q '172.30.77.12'
compose exec -T control ip addr add 192.0.2.200/32 dev eth0
for service in panel probe client node1 node2; do
 compose exec -T "$service" ip route replace 192.0.2.200/32 via 172.30.77.3
 done
python3 deploy/network-e2e/test.py
