#!/bin/sh
# postrm vpnctl-sing-box: purge removes the node config and state (ACME).
set -e
if [ "$1" = purge ]; then
  rm -rf /etc/vpnctl/sing-box /var/lib/vpnctl/sing-box
  rmdir /etc/vpnctl /var/lib/vpnctl 2>/dev/null || true # in case the other package is removed too
fi
if [ -d /run/systemd/system ]; then
  systemctl daemon-reload || true
fi
