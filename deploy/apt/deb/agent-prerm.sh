#!/bin/sh
# prerm vpnctl-agent: on removal (not upgrade) stop and disable the agent.
set -e
if [ "$1" = remove ] && [ -d /run/systemd/system ]; then
  systemctl disable --now vpnctl-agent.service >/dev/null 2>&1 || true
fi
