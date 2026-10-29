#!/bin/sh
# postinst vpnctl-sing-box. The service is enabled but starts by itself only once the agent
# writes a config (ConditionPathExists in the unit); on upgrade it restarts with the new binary.
set -e
install -d -m 0755 /etc/vpnctl/sing-box
if [ -d /run/systemd/system ]; then
  systemctl daemon-reload
  systemctl enable vpnctl-sing-box.service >/dev/null 2>&1 || true
  if [ "$1" = configure ] && [ -n "${2:-}" ]; then
    systemctl try-restart vpnctl-sing-box.service || true
  fi
fi
