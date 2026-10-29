#!/bin/sh
# postrm vpnctl-panel: purge removes the settings. The database and PKI (/var/lib/vpnctl/panel) remain:
# they hold the users and the node CA, delete them only by hand.
set -e
if [ "$1" = purge ]; then
  rm -f /etc/vpnctl/panel.env
  rmdir /etc/vpnctl 2>/dev/null || true
fi
if [ -d /run/systemd/system ]; then
  systemctl daemon-reload || true
fi
