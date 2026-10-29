#!/bin/sh
set -eu
export PANEL_PROBE_TOKEN=local-probe-token PANEL_SINGBOX_BIN=/work/sing-box PANEL_PROBE_CHALLENGE_URL=https://192.0.2.200:8443/health/challenge PANEL_PROBE_CA=/work/pki/ca.crt
exec /work/panel probe -listen :9444 -cert /work/pki/server.crt -key /work/pki/server.key -allow '172.30.77.11:443/vless-reality,172.30.77.11:8443/hysteria2,172.30.77.11:51820/wireguard,172.30.77.12:443/vless-reality,172.30.77.12:8443/hysteria2,172.30.77.12:51820/wireguard,172.30.77.21:443/vless-reality,172.30.77.21:8443/hysteria2,172.30.77.21:51820/wireguard'
