#!/bin/sh
# postinst vpnctl-panel: the vpnctl user, the data directory, /etc/vpnctl/panel.env with
# random secrets (first install only), a private CA. The panel starts once the server
# certificate exists (ConditionPathExists in the unit).
set -e
STATE=/var/lib/vpnctl/panel
ENV=/etc/vpnctl/panel.env

if ! getent passwd vpnctl >/dev/null; then
  useradd --system --user-group --home-dir "$STATE" --no-create-home --shell /usr/sbin/nologin vpnctl
fi
install -d -m 0755 /var/lib/vpnctl /etc/vpnctl
install -d -m 0750 -o vpnctl -g vpnctl "$STATE"

as_vpnctl() {
  if command -v runuser >/dev/null 2>&1; then runuser -u vpnctl -- "$@"; else su -s /bin/sh vpnctl -c "$*"; fi
}
rnd() { od -An -N"$1" -tx1 /dev/urandom | tr -d ' \n'; }

FIRST=
if [ ! -f "$ENV" ]; then
  FIRST=1
  ADMIN_PATH=/$(rnd 8)
  ADMIN_PASSWORD=$(rnd 12)
  sed -e "s|@ADMIN_PATH@|$ADMIN_PATH|" -e "s|@ADMIN_PASSWORD@|$ADMIN_PASSWORD|" -e "s|@SESSION_SECRET@|$(rnd 32)|" \
    /usr/share/vpnctl/panel.env.example > "$ENV.new"
  chown root:vpnctl "$ENV.new"
  chmod 0640 "$ENV.new"
  mv "$ENV.new" "$ENV"
fi

if [ ! -f "$STATE/pki/ca.crt" ]; then
  as_vpnctl /usr/bin/vpnctl-panel ca init -dir "$STATE/pki"
fi

if [ -d /run/systemd/system ]; then
  systemctl daemon-reload
  systemctl enable vpnctl-panel.service >/dev/null 2>&1 || true
  if [ -f "$STATE/pki/server.crt" ]; then
    systemctl restart vpnctl-panel.service || true
  fi
fi

if [ -n "$FIRST" ]; then
  cat <<MSG

vpnctl-panel installed. Admin interface: <PANEL_PUBLIC_URL>$ADMIN_PATH/
  password: $ADMIN_PASSWORD   (both are in $ENV)
Next steps:
  1. set PANEL_PUBLIC_URL in $ENV and put a reverse proxy with HTTPS in front of 127.0.0.1:8080 and :8081
  2. sudo -u vpnctl vpnctl-panel ca server -dir $STATE/pki -host <panel domain>,127.0.0.1
  3. systemctl start vpnctl-panel
  4. this machine as a node too: sudo vpnctl-panel local-node -host <domain for clients>

MSG
fi
