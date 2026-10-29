#!/bin/sh
# Installs the agent on a node (Debian/Ubuntu) from the vpnctl-agent and vpnctl-sing-box deb packages.
# Run as root on the node itself, from the unpacked agent archive (panel -> node -> "Download agent archive"):
#
#   ./install-agent.sh --panel panel.example.com:9443[,spare.example.com:9443] [--repo URL]
#   ./install-agent.sh --panel ... --deb vpnctl-agent_*.deb --deb vpnctl-sing-box_*.deb   # without network access to the repository
#   ./install-agent.sh --panel ... --firewall 443/tcp,8443/udp [--ssh-port 22]   # ufw: only SSH and these ports
#
# Node certificates are taken from the script directory (ca.crt, agent.crt, agent.key), other paths via --ca/--cert/--key.
# A repeated run updates the packages, certificates and panel address; the rest of /etc/vpnctl/agent.env is left alone.
set -eu

usage() { sed -n '2,11p' "$0" | sed 's/^# \{0,1\}//'; exit 1; }

SRC=$(cd "$(dirname "$0")" && pwd)
PANEL= REPO=https://artemlavr.github.io/giocondo DEBS= FIREWALL= SSH_PORT=22
CA=$SRC/ca.crt CERT=$SRC/agent.crt KEY=$SRC/agent.key
while [ $# -gt 0 ]; do
  case "$1" in
    --panel) PANEL=$2; shift 2 ;;
    --repo) REPO=${2%/}; shift 2 ;;
    --deb) case "$2" in /*) d=$2 ;; *) d=$PWD/$2 ;; esac; DEBS="$DEBS $d"; shift 2 ;;
    --firewall) FIREWALL=$2; shift 2 ;;
    --ssh-port) SSH_PORT=$2; shift 2 ;;
    --ca) CA=$2; shift 2 ;;
    --cert) CERT=$2; shift 2 ;;
    --key) KEY=$2; shift 2 ;;
    -h|--help) usage ;;
    *) echo "unknown option: $1" >&2; usage ;;
  esac
done
[ -n "$PANEL" ] || usage
for f in "$CA" "$CERT" "$KEY"; do [ -f "$f" ] || { echo "no such file: $f" >&2; exit 1; }; done
[ "$(id -u)" -eq 0 ] || { echo "run as root" >&2; exit 1; }
export DEBIAN_FRONTEND=noninteractive

if [ -n "$DEBS" ]; then
  # shellcheck disable=SC2086 # list of paths
  apt-get install -y $DEBS
else
  KEYRING=/usr/share/keyrings/vpnctl.gpg
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL "$REPO/vpnctl.gpg" -o "$KEYRING.new"
  else
    wget -qO "$KEYRING.new" "$REPO/vpnctl.gpg"
  fi
  mv "$KEYRING.new" "$KEYRING" && chmod 0644 "$KEYRING"
  echo "deb [signed-by=$KEYRING] $REPO stable main" > /etc/apt/sources.list.d/vpnctl.list
  apt-get update -o Dir::Etc::sourcelist=sources.list.d/vpnctl.list -o Dir::Etc::sourceparts=- -o APT::Get::List-Cleanup=0
  apt-get install -y vpnctl-agent
fi
/usr/lib/vpnctl/sing-box version | head -1

install -d -m 0700 /etc/vpnctl/agent
install -m 0644 "$CA" /etc/vpnctl/agent/ca.crt
install -m 0644 "$CERT" /etc/vpnctl/agent/agent.crt
install -m 0600 "$KEY" /etc/vpnctl/agent/agent.key

ENV=/etc/vpnctl/agent.env
[ -f "$ENV" ] || install -m 0600 /usr/share/vpnctl/agent.env.example "$ENV"
if grep -q '^AGENT_PANEL_ADDR=' "$ENV"; then
  sed -i "s|^AGENT_PANEL_ADDR=.*|AGENT_PANEL_ADDR=$PANEL|" "$ENV"
else
  echo "AGENT_PANEL_ADDR=$PANEL" >> "$ENV"
fi

if [ -n "$FIREWALL" ]; then
  # SSH first, otherwise enabling ufw would drop the current session.
  command -v ufw >/dev/null 2>&1 || apt-get install -y ufw
  ufw allow "$SSH_PORT/tcp" >/dev/null
  for rule in $(echo "$FIREWALL" | tr ',' ' '); do
    ufw allow "$rule" >/dev/null
  done
  ufw --force enable >/dev/null
  echo "firewall: allowed $SSH_PORT/tcp $FIREWALL"
fi

if [ -d /run/systemd/system ]; then
  # The agent starts vpnctl-sing-box once it gets a config from the panel.
  systemctl enable vpnctl-sing-box.service vpnctl-agent.service >/dev/null 2>&1
  systemctl restart vpnctl-agent.service
  echo "agent installed; logs: journalctl -u vpnctl-agent -f"
else
  echo "agent installed; systemd is not running, start /usr/bin/vpnctl-agent yourself"
fi
