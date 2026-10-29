#!/bin/sh
# postinst vpnctl-agent. The env file is created only if missing (not a conffile: a package
# upgrade does not ask about the changed file). The agent starts only when the node certificate exists.
set -e
install -d -m 0700 /etc/vpnctl/agent
if [ ! -f /etc/vpnctl/agent.env ]; then
  install -m 0600 /usr/share/vpnctl/agent.env.example /etc/vpnctl/agent.env
fi
if [ -d /run/systemd/system ]; then
  systemctl daemon-reload
  systemctl enable vpnctl-agent.service >/dev/null 2>&1 || true
  if [ -f /etc/vpnctl/agent/agent.crt ]; then
    systemctl restart vpnctl-agent.service || true
  fi
fi
