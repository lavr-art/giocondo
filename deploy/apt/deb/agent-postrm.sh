#!/bin/sh
# postrm giocondo-vpn-agent: purge removes the node certificates and env.
set -e
if [ "$1" = purge ]; then
  rm -rf /etc/giocondo-vpn/agent /etc/giocondo-vpn/agent.env /var/lib/giocondo-vpn/agent
  rmdir /etc/giocondo-vpn /var/lib/giocondo-vpn 2>/dev/null || true # in case the other package is removed too
fi
if [ -d /run/systemd/system ]; then
  systemctl daemon-reload || true
fi
