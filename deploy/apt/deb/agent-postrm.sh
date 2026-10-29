#!/bin/sh
# postrm vpnctl-agent: purge removes the node certificates and env.
set -e
if [ "$1" = purge ]; then
  rm -rf /etc/vpnctl/agent /etc/vpnctl/agent.env
  rmdir /etc/vpnctl /var/lib/vpnctl 2>/dev/null || true # in case the other package is removed too
fi
if [ -d /run/systemd/system ]; then
  systemctl daemon-reload || true
fi
