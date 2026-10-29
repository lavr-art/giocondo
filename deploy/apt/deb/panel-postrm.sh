#!/bin/sh
# postrm giocondo-vpn-panel: purge removes the settings. The database and PKI (/var/lib/giocondo-vpn/panel) remain:
# they hold the users and the node CA, delete them only by hand.
set -e
if [ "$1" = purge ]; then
  rm -f /etc/giocondo-vpn/panel.env
  rmdir /etc/giocondo-vpn 2>/dev/null || true
fi
if [ -d /run/systemd/system ]; then
  systemctl daemon-reload || true
fi
