#!/bin/sh
set -eu
export PANEL_DB=/work/panel.db PANEL_PKI_DIR=/work/pki
export PANEL_LISTEN=0.0.0.0:8080 PANEL_SUB_LISTEN=0.0.0.0:8081 PANEL_GRPC_LISTEN=:9443
export PANEL_AGENT_ADDR=panel:9443 PANEL_ADMIN_PASSWORD=network-test-only PANEL_SESSION_SECRET=network-test-session-secret PANEL_ADMIN_PATH=/test-admin
export PANEL_PUBLIC_URL=https://control:8443 PANEL_APT_REPO=http://control:8000/repo
export PANEL_DNS_DOMAIN=vpn.test PANEL_CLOUDFLARE_ZONE_ID=test PANEL_CLOUDFLARE_API_TOKEN=local-test-token PANEL_DNS_API_URL=http://control:8000
export PANEL_DNS_VERIFY_SERVER=172.30.77.3:53 PANEL_SINGBOX_BIN=/work/sing-box PANEL_PROBE_CHALLENGE_URL=https://192.0.2.200:8443/health/challenge PANEL_PROBE_CA=/work/pki/ca.crt
export PANEL_PROBE_URL=https://probe:9444/ PANEL_PROBE_TOKEN=local-probe-token
export PANEL_WEBHOOK_URL=http://control:8000/events
export PANEL_HEALTH_INTERVAL=${PANEL_HEALTH_INTERVAL:-3s}
/work/panel token create -name e2e -scope admin > /work/token-output
chmod 600 /work/token-output
exec /work/panel
