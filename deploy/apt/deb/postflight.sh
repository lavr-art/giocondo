#!/bin/sh
# Called by the package postinst after its normal service setup.
set -eu
role=${1:?package role is required}
[ -d /run/systemd/system ] || exit 0
[ -z "${GIOCONDO_RESTORE_FROM:-}" ] || exit 0
if [ "${GIOCONDO_REQUIRE_READY:-0}" != 1 ]; then
case "$role" in
  panel) [ -f /var/lib/giocondo-vpn/panel/pki/server.crt ] || exit 0 ;;
  agent) [ -f /etc/giocondo-vpn/agent/agent.crt ] || exit 0 ;;
  sing-box) [ -f /etc/giocondo-vpn/sing-box/config.json ] || exit 0 ;;
  *) exit 1 ;;
esac
fi
service=giocondo-vpn-$role
failure() {
  echo "$service readiness failed; see journalctl -u $service; recovery snapshots: /var/lib/giocondo-vpn/backups" >&2
  exit 1
}
systemctl is-active --quiet "$service" || failure
pid=$(systemctl show -p MainPID --value "$service")
case "$role" in
  panel) /usr/bin/giocondo-vpn-panel check -pid "$pid" -timeout 30s || failure ;;
  agent)
    i=0
    until /usr/bin/giocondo-vpn-agent -check -pid "$pid"; do
      i=$((i+1)); [ "$i" -lt 30 ] || failure
      sleep 1
    done ;;
  sing-box) /usr/lib/giocondo-vpn/sing-box check -c /etc/giocondo-vpn/sing-box/config.json >/dev/null 2>&1 || failure ;;
esac
