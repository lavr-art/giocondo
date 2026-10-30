#!/bin/sh
# postinst giocondo-vpn-sing-box. The service is enabled but starts by itself only once the agent
# writes a config (ConditionPathExists in the unit); on upgrade it restarts with the new binary.
set -e
[ -z "${GIOCONDO_RESTORE_FROM:-}" ] || exit 0
install -d -m 0755 /etc/giocondo-vpn/sing-box
if [ -d /run/systemd/system ]; then
  systemctl daemon-reload
  systemctl enable giocondo-vpn-sing-box.service >/dev/null 2>&1 || true
  if [ "$1" = configure ] && [ -n "${2:-}" ]; then
    if [ -f /etc/giocondo-vpn/sing-box/config.json ]; then
      /usr/lib/giocondo-vpn/sing-box check -c /etc/giocondo-vpn/sing-box/config.json >/dev/null 2>&1
    fi
    systemctl try-restart giocondo-vpn-sing-box.service
  fi
fi

/usr/share/giocondo-vpn/sing-box/postflight.sh sing-box
