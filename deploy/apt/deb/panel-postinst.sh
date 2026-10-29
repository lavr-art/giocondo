#!/bin/sh
# postinst giocondo-vpn-panel: the giocondo-vpn user, the data directory, /etc/giocondo-vpn/panel.env with
# random secrets (first install only), a private CA. The panel starts once the server
# certificate exists (ConditionPathExists in the unit).
set -e
STATE=/var/lib/giocondo-vpn/panel
ENV=/etc/giocondo-vpn/panel.env

if ! getent passwd giocondo-vpn >/dev/null; then
  useradd --system --user-group --home-dir "$STATE" --no-create-home --shell /usr/sbin/nologin giocondo-vpn
fi
install -d -m 0755 /var/lib/giocondo-vpn /etc/giocondo-vpn
install -d -m 0750 -o giocondo-vpn -g giocondo-vpn "$STATE"

as_service_user() {
  if command -v runuser >/dev/null 2>&1; then runuser -u giocondo-vpn -- "$@"; else su -s /bin/sh giocondo-vpn -c "$*"; fi
}
rnd() { od -An -N"$1" -tx1 /dev/urandom | tr -d ' \n'; }

FIRST=
if [ ! -f "$ENV" ]; then
  FIRST=1
  ADMIN_PATH=/$(rnd 8)
  ADMIN_PASSWORD=$(rnd 12)
  sed -e "s|@ADMIN_PATH@|$ADMIN_PATH|" -e "s|@ADMIN_PASSWORD@|$ADMIN_PASSWORD|" -e "s|@SESSION_SECRET@|$(rnd 32)|" \
    /usr/share/giocondo-vpn/panel.env.example > "$ENV.new"
  chown root:giocondo-vpn "$ENV.new"
  chmod 0640 "$ENV.new"
  mv "$ENV.new" "$ENV"
fi

if [ ! -f "$STATE/pki/ca.crt" ]; then
  as_service_user /usr/bin/giocondo-vpn-panel ca init -dir "$STATE/pki"
fi

if [ -d /run/systemd/system ]; then
  systemctl daemon-reload
  systemctl enable giocondo-vpn-panel.service >/dev/null 2>&1 || true
  if [ -f "$STATE/pki/server.crt" ]; then
    systemctl restart giocondo-vpn-panel.service || true
  fi
fi

if [ -n "$FIRST" ]; then
  cat <<MSG

giocondo-vpn-panel installed. Admin interface: <PANEL_PUBLIC_URL>$ADMIN_PATH/
  password: $ADMIN_PASSWORD   (both are in $ENV)
Next steps:
  1. set PANEL_PUBLIC_URL in $ENV and put a reverse proxy with HTTPS in front of 127.0.0.1:8080 and :8081
  2. sudo -u giocondo-vpn giocondo-vpn-panel ca server -dir $STATE/pki -host <panel domain>,127.0.0.1
  3. systemctl start giocondo-vpn-panel
  4. this machine as a node too: sudo giocondo-vpn-panel local-node -host <domain for clients>

MSG
fi
