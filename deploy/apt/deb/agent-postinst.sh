#!/bin/sh
# postinst giocondo-vpn-agent. The env file is created only if missing (not a conffile: a package
# upgrade does not ask about the changed file). The agent starts only when the node certificate exists.
set -e
install -d -m 0700 /etc/giocondo-vpn/agent
if [ ! -f /etc/giocondo-vpn/agent.env ]; then
  install -m 0600 /usr/share/giocondo-vpn/agent.env.example /etc/giocondo-vpn/agent.env
fi
if [ -d /run/systemd/system ]; then
  systemctl daemon-reload
  systemctl enable giocondo-vpn-agent.service >/dev/null 2>&1 || true
  if [ -f /etc/giocondo-vpn/agent/agent.crt ]; then
    systemctl restart giocondo-vpn-agent.service || true
  fi
fi
