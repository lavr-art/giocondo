#!/bin/sh
set -eu
cd "$(dirname "$0")/../.."
compose() { docker compose -f test/network-e2e/compose.yaml "$@"; }
case "${1:-test}" in
 down) compose down --remove-orphans; exit;;
 logs) compose logs --tail=100; exit;;
 test|fresh|installer|upgrade|cadence|agent-upgrade) ;;
 *) echo 'Usage: run.sh [test|fresh|installer|upgrade|cadence|agent-upgrade|logs|down]' >&2; exit 2;;
esac
MODE=${1:-test}
run_test() {
 go test -tags=e2e -count=1 -v ./test/network-e2e -run "^$1\$" -timeout 45m
}
# These acceptance checks continue a successful network scenario without resetting it.
case "$MODE" in
 cadence)
  [ "${PANEL_HEALTH_INTERVAL:-}" = 5m ] || { echo 'Use PANEL_HEALTH_INTERVAL=5m for the cadence check' >&2; exit 1; }
  run_test TestCadence; exit;;
 agent-upgrade)
  [ -f bin/network-e2e/agent-legacy ] || { echo 'Build the previous Linux agent into bin/network-e2e/agent-legacy first' >&2; exit 1; }
  run_test TestAgentUpgrade; exit;;
esac
ARCH=$(docker info --format '{{.Architecture}}')
case "$ARCH" in aarch64|arm64) ARCH=arm64;;x86_64|amd64) ARCH=amd64;;*) echo 'Unsupported Docker architecture' >&2;exit 1;;esac
mkdir -p bin/network-e2e bin/network-e2e-tools
CGO_ENABLED=0 go build -o bin/network-e2e-tools/bench ./test/network-e2e/cmd/bench
CGO_ENABLED=0 GOOS=linux GOARCH="$ARCH" go build -o bin/network-e2e/bench ./test/network-e2e/cmd/bench
# Build with `make deb` first, or point CANDIDATE_DEBS at downloaded candidate artifacts.
bin/network-e2e-tools/bench prepare-packages "$ARCH" "$MODE"
if [ "${1:-}" = fresh ]; then
 compose down --remove-orphans
fi
compose build setup
if [ "$MODE" = fresh ]; then
 # Rename through the container filesystem so Docker Desktop invalidates old SQLite inodes.
 compose run --rm setup /work/bench archive /work
fi
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
  run_test TestUpgrade
 else
  run_test TestInstaller
 fi
 exit
fi
compose up -d control panel node1 node2 client probe
compose exec -T node1 ip addr replace 172.30.77.12/24 dev eth0
compose exec -T control ip addr replace 192.0.2.200/32 dev eth0
for service in panel probe client node1 node2; do
 compose exec -T "$service" ip route replace 192.0.2.200/32 via 172.30.77.3
 done
run_test TestNetwork
