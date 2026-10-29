#!/bin/sh
# prerm vpnctl-panel: on removal (not upgrade) stop and disable the panel.
set -e
if [ "$1" = remove ] && [ -d /run/systemd/system ]; then
  systemctl disable --now vpnctl-panel.service >/dev/null 2>&1 || true
fi
