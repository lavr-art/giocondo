#!/bin/sh
# prerm giocondo-vpn-sing-box: on removal (not upgrade) stop and disable the service.
set -e
if [ "$1" = remove ] && [ -d /run/systemd/system ]; then
  systemctl disable --now giocondo-vpn-sing-box.service >/dev/null 2>&1 || true
fi
