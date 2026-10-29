#!/bin/sh
# postrm giocondo-vpn-sing-box: purge removes the node config and state (ACME).
set -e
if [ "$1" = purge ]; then
  rm -rf /etc/giocondo-vpn/sing-box /var/lib/giocondo-vpn/sing-box
  rmdir /etc/giocondo-vpn /var/lib/giocondo-vpn 2>/dev/null || true # in case the other package is removed too
fi
if [ -d /run/systemd/system ]; then
  systemctl daemon-reload || true
fi
