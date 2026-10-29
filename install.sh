#!/bin/sh
# Installs giocondo-vpn on a clean Debian/Ubuntu server: the panel and a node on the same machine.
#
#   bash <(curl -fsSL https://lavr-art.github.io/giocondo/install.sh) [install] [options]
#   ... status
#   ... uninstall [--purge]
#
# Options:
#   --domain D    HTTPS through Caddy (Let's Encrypt) at https://D:8443; port 80 must be reachable
#   --ip A        public IPv4 of the server (default: source address of the default route)
#   --http        serve subscriptions over plain HTTP at http://IP:8081 instead of HTTPS by IP
#   --version V   install exactly this package version
#   --repo URL    apt repository (default: https://lavr-art.github.io/giocondo)
#   --reconfigure explicitly change an existing public address / HTTP or HTTPS mode
#   --yes         do not ask questions
#
# Without --domain subscriptions are served at https://IP:8443 with a short-lived Let's Encrypt IP certificate
# (port 80 must stay reachable for renewal) and the admin UI listens on 127.0.0.1 only (open it through an SSH tunnel).
# Every step checks the current state first, so running the script again changes nothing.
set -eu

REPO=https://lavr-art.github.io/giocondo
ENV_FILE=/etc/giocondo-vpn/panel.env
STATE=/var/lib/giocondo-vpn/panel
PKI=$STATE/pki
AGENT_CERT=/etc/giocondo-vpn/agent/agent.crt
KEYRING=/usr/share/keyrings/giocondo-vpn.gpg
SOURCES=/etc/apt/sources.list.d/giocondo-vpn.list
CADDYFILE=/etc/caddy/Caddyfile
CADDY_MARKER='# Managed by giocondo-vpn install.sh'
INSTALL_STATE=/var/lib/giocondo-vpn/installer
CA_BUNDLE=/etc/ssl/certs/ca-certificates.crt
# Earlier Caddy releases refuse Let's Encrypt IP identifiers (caddyserver/caddy#7399).
CADDY_IP_MIN=2.10.2
PACKAGES='giocondo-vpn-panel giocondo-vpn-agent giocondo-vpn-sing-box'

log() { printf '==> %s\n' "$*"; }
warn() { printf 'WARNING: %s\n' "$*" >&2; }
die() { printf 'ERROR: %s\n' "$*" >&2; exit 1; }

usage() {
  cat <<'EOF'
usage: install.sh [install] [--domain D | --http] [--ip A] [--reconfigure] [--version V] [--repo URL] [--yes]
       install.sh status
       install.sh uninstall [--purge] [--yes]
EOF
  exit "${1:-0}"
}

# confirm asks on the terminal; without a terminal it fails unless --yes was given.
confirm() {
  [ "$YES" = 1 ] && return 0
  if (: </dev/tty) 2>/dev/null; then
    printf '%s [y/N] ' "$1" >/dev/tty
    read -r answer </dev/tty || answer=
    case "$answer" in y|Y|yes|YES) return 0 ;; esac
    return 1
  fi
  die "$1 (no terminal: pass --yes)"
}

download() {
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL "$1" -o "$2"
  else
    wget -qO "$2" "$1"
  fi
}

# replace_if_changed moves $1 over $2 only when the contents differ; returns 0 if $2 changed.
replace_if_changed() {
  if [ -f "$2" ] && cmp -s "$1" "$2"; then
    rm -f "$1"
    return 1
  fi
  mv "$1" "$2"
  return 0
}

check_system() {
  [ "$(id -u)" -eq 0 ] || die "run as root"
  [ -r /etc/os-release ] || die "unsupported system: no /etc/os-release"
  # shellcheck disable=SC1091 # host OS metadata, not a repository file
  os_id=$(. /etc/os-release && echo "${ID:-}")
  # shellcheck disable=SC1091
  major=$(. /etc/os-release && echo "${VERSION_ID%%.*}")
  case "$os_id" in
    debian) [ "${major:-0}" -ge 11 ] || die "Debian 11 or newer is required" ;;
    ubuntu) [ "${major:-0}" -ge 22 ] || die "Ubuntu 22.04 or newer is required" ;;
    *) die "unsupported system ${os_id:-unknown}: Debian 11+ or Ubuntu 22.04+ is required" ;;
  esac
  arch=$(dpkg --print-architecture)
  case "$arch" in amd64|arm64) ;; *) die "unsupported architecture $arch: amd64 or arm64 is required" ;; esac
  [ -d /run/systemd/system ] || die "systemd is required"
}

detect_ip() {
  ip -4 route get 1.1.1.1 2>/dev/null | awk '{for (i = 1; i < NF; i++) if ($i == "src") { print $(i + 1); exit }}'
}

prepare_dependencies() {
  export DEBIAN_FRONTEND=noninteractive
  deps=
  if ! dpkg-query -W -f='${Status}' ca-certificates 2>/dev/null | grep -q '^install ok installed$'; then
    deps=ca-certificates
  fi
  # The IP certificate check needs curl --connect-to.
  if ! command -v curl >/dev/null 2>&1 && { [ "$MODE" = ip ] || ! command -v wget >/dev/null 2>&1; }; then
    deps="$deps curl"
  fi
  if ! command -v openssl >/dev/null 2>&1; then deps="$deps openssl"; fi
  if [ -n "$deps" ]; then
    apt-get update -qq
    # shellcheck disable=SC2086 # package list
    apt-get install -y -qq $deps >/dev/null
  fi
  if ! valid_ca_bundle; then update-ca-certificates --fresh >/dev/null; fi
  valid_ca_bundle || die "CA bundle is invalid after update-ca-certificates"
}

valid_ca_bundle() {
  [ -s "$CA_BUNDLE" ] && grep -q -- '-----BEGIN CERTIFICATE-----' "$CA_BUNDLE" &&
    openssl crl2pkcs7 -nocrl -certfile "$CA_BUNDLE" -out /dev/null 2>/dev/null
}

setup_repository() {
  prepare_dependencies
  tmp=$(mktemp)
  download "$REPO/giocondo-vpn.gpg" "$tmp" || { rm -f "$tmp"; die "cannot download $REPO/giocondo-vpn.gpg"; }
  chmod 0644 "$tmp"
  replace_if_changed "$tmp" "$KEYRING" && log "apt key updated" || true
  tmp=$(mktemp)
  echo "deb [signed-by=$KEYRING] $REPO stable main" >"$tmp"
  chmod 0644 "$tmp"
  replace_if_changed "$tmp" "$SOURCES" && log "apt source updated" || true
}

install_packages() {
  log "installing packages"
  apt-get update -qq
  list=
  for p in $PACKAGES; do
    if [ -n "$PKG_VERSION" ]; then list="$list $p=$PKG_VERSION"; else list="$list $p"; fi
  done
  # shellcheck disable=SC2086 # list of packages
  apt-get install -y -qq openssl $list
}

# set_env sets KEY=VALUE in panel.env: replaces an existing line, then a commented example, otherwise appends.
set_env() {
  env_tmp=$(mktemp "$(dirname "$ENV_FILE")/.panel-env.XXXXXX")
  cp -p "$ENV_FILE" "$env_tmp"
  ENV_KEY=$1 ENV_VALUE=$2 awk '
    BEGIN { key=ENVIRON["ENV_KEY"]; value=ENVIRON["ENV_VALUE"] }
    $0 ~ "^" key "=" || $0 ~ "^# *" key "=" { if (!done++) print key "=" value; next }
    { print }
    END { if (!done) print key "=" value }
  ' "$ENV_FILE" >"$env_tmp"
  if cmp -s "$env_tmp" "$ENV_FILE"; then rm -f "$env_tmp"; else mv "$env_tmp" "$ENV_FILE"; fi
}

read_env() {
  [ -f "${2:-$ENV_FILE}" ] || return 0
  ENV_KEY=$1 awk 'index($0,ENVIRON["ENV_KEY"] "=")==1 {value=substr($0,length(ENVIRON["ENV_KEY"])+2)}
    END {sub(/^"/,"",value);sub(/"$/,"",value);print value}' "${2:-$ENV_FILE}"
}

prepare_config() {
  EXISTING=0
  OLD_PUBLIC=$(read_env PANEL_PUBLIC_URL)
  OLD_SUB=$(read_env PANEL_SUB_URL)
  if [ -n "$OLD_PUBLIC$OLD_SUB" ] || [ -f "$AGENT_CERT" ]; then EXISTING=1; fi
  [ "$DOMAIN_SET$HTTP_SET" != 11 ] || die "--domain and --http cannot be combined"
  if [ "$RECONFIGURE" = 1 ] && [ "$DOMAIN_SET$IP_SET$HTTP_SET" = 000 ]; then
    die "--reconfigure requires --domain, --ip or --http"
  fi
  requested_mode=
  if [ "$DOMAIN_SET" = 1 ]; then requested_mode=domain; elif [ "$HTTP_SET" = 1 ]; then requested_mode=http; fi
  MODE=${requested_mode:-ip}
  if [ "$EXISTING" = 1 ] && [ "$RECONFIGURE" = 0 ]; then
    old_host=$(printf '%s\n' "$OLD_SUB" | sed 's|^[a-z]*://||;s|[:/].*||')
    old_domain='' old_mode=http
    case "$OLD_SUB" in https://*)
      case "$old_host" in *[!0-9.]*) old_mode=domain old_domain=$old_host ;; *) old_mode=ip ;; esac ;;
    esac
    if [ -n "$requested_mode" ] && [ "$requested_mode" != "$old_mode" ]; then
      die "installed mode is $old_mode: use --reconfigure to change it"
    fi
    if [ "$DOMAIN_SET" = 1 ] && [ "$DOMAIN" != "$old_domain" ]; then
      die "public domain differs: use --reconfigure --domain to change it"
    fi
    old_ip=
    if [ -f "$INSTALL_STATE/ip" ]; then old_ip=$(cat "$INSTALL_STATE/ip"); fi
    case "$old_host" in *[!0-9.]*|'') ;; *) old_ip=$old_host ;; esac
    if [ "$IP_SET" = 1 ] && [ -n "$old_ip" ] && [ "$IP" != "$old_ip" ]; then
      die "public IP differs: use --reconfigure --ip to change it"
    fi
    DOMAIN=$old_domain MODE=$old_mode
    [ -n "$IP" ] || IP=$old_ip
  fi
  [ -n "$IP" ] || IP=$(detect_ip)
  [ -n "$IP" ] || die "cannot detect the server IP: pass --ip"
  case "$IP" in *[!0-9.]*|'') die "--ip must be an IPv4 address" ;; esac
  case "$DOMAIN" in *[!a-zA-Z0-9.-]*) die "invalid domain" ;; esac
  if uses_caddy; then check_caddy_ownership; fi
}

uses_caddy() { [ "$MODE" != http ]; }

configure_panel() {
  [ -f "$ENV_FILE" ] || die "$ENV_FILE is missing: the giocondo-vpn-panel package did not finish installing"
  if [ "$EXISTING" = 1 ] && [ "$RECONFIGURE" = 0 ]; then return; fi
  before=$(cksum <"$ENV_FILE")
  admin_listen=$(read_env PANEL_LISTEN)
  sub_listen=$(read_env PANEL_SUB_LISTEN)
  admin_port=${admin_listen##*:}
  sub_port=${sub_listen##*:}
  admin_port=${admin_port:-8080}
  sub_port=${sub_port:-8081}
  case "$MODE" in
    domain)
      set_env PANEL_PUBLIC_URL "https://$DOMAIN:8443"
      set_env PANEL_SUB_URL "https://$DOMAIN:8443"
      set_env PANEL_SUB_LISTEN "127.0.0.1:$sub_port" ;;
    ip)
      set_env PANEL_PUBLIC_URL "http://127.0.0.1:$admin_port"
      set_env PANEL_SUB_URL "https://$IP:8443"
      set_env PANEL_SUB_LISTEN "127.0.0.1:$sub_port" ;;
    *)
      set_env PANEL_PUBLIC_URL "http://127.0.0.1:$admin_port"
      set_env PANEL_SUB_URL "http://$IP:$sub_port"
      set_env PANEL_SUB_LISTEN "0.0.0.0:$sub_port" ;;
  esac
  if [ "$EXISTING" = 0 ]; then
    if [ -z "$(read_env PANEL_LISTEN)" ]; then set_env PANEL_LISTEN 127.0.0.1:8080; fi
    if [ -z "$(read_env PANEL_GRPC_LISTEN)" ]; then set_env PANEL_GRPC_LISTEN 127.0.0.1:9443; fi
  fi
  [ "$before" = "$(cksum <"$ENV_FILE")" ] || { PANEL_CHANGED=1; log "panel settings updated"; }
}

cert_covers() {
  case "$1" in
    *[!0-9.]*) openssl x509 -in "$PKI/server.crt" -noout -checkhost "$1" 2>/dev/null | grep -q 'does match' ;;
    *) openssl x509 -in "$PKI/server.crt" -noout -checkip "$1" 2>/dev/null | grep -q 'does match' ;;
  esac
}

issue_panel_cert() {
  if [ "$EXISTING" = 1 ]; then
    if [ ! -s "$PKI/server.crt" ] || ! cert_covers 127.0.0.1; then
      die "existing panel certificate is missing or does not cover localhost; repair it explicitly"
    fi
    return
  fi
  host=${DOMAIN:-$IP}
  if [ -f "$PKI/server.crt" ] && cert_covers 127.0.0.1 && cert_covers "$host"; then
    return
  fi
  log "issuing the panel certificate for 127.0.0.1,$host"
  runuser -u giocondo-vpn -- giocondo-vpn-panel ca server -dir "$PKI" -host "127.0.0.1,$host" >/dev/null
  PANEL_CHANGED=1
}

caddy_version() { caddy version 2>/dev/null | awk '{sub(/^v/, "", $1); print $1; exit}'; }

# version_at_least succeeds when version $1 (a Debian version is reduced to upstream) is $2 or newer.
version_at_least() {
  v=${1#*:}
  v=${v%%-*}
  [ -n "$v" ] && [ "$(printf '%s\n%s\n' "$2" "$v" | sort -V | head -n 1)" = "$2" ]
}

caddy_ready() {
  command -v caddy >/dev/null 2>&1 || return 1
  [ "$MODE" != ip ] || version_at_least "$(caddy_version)" "$CADDY_IP_MIN"
}

install_caddy() {
  caddy_ready && return
  log "installing Caddy"
  candidate=$(apt-cache policy caddy 2>/dev/null | awk '/Candidate:/ {print $2}')
  if [ -z "$candidate" ] || [ "$candidate" = '(none)' ] ||
    { [ "$MODE" = ip ] && ! version_at_least "$candidate" "$CADDY_IP_MIN"; }; then
    # Ubuntu 22.04 has no caddy package and distribution packages are too old for IP certificates:
    # use the official signed repository.
    apt-get install -y -qq gnupg >/dev/null
    tmp=$(mktemp)
    download https://dl.cloudsmith.io/public/caddy/stable/gpg.key "$tmp"
    gpg --batch --yes --dearmor -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg "$tmp"
    rm -f "$tmp"
    download https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt /etc/apt/sources.list.d/caddy-stable.list
    apt-get update -qq
  fi
  apt-get install -y -qq caddy >/dev/null
  caddy_ready || die "Caddy $CADDY_IP_MIN or newer is required for an IP certificate, found $(caddy_version): upgrade it or use --http"
}

configure_caddy() {
  install_caddy
  check_caddy_ownership
  tmp=$(mktemp "$(dirname "$CADDYFILE")/.giocondo-caddy.XXXXXX")
  { echo "$CADDY_MARKER; changes are overwritten on the next run."; caddy_site; } >"$tmp"
  chmod 0644 "$tmp"
  caddy validate --config "$tmp" --adapter caddyfile >/dev/null 2>&1 || { rm -f "$tmp"; die "generated Caddyfile is invalid"; }
  caddy_backup=
  if [ -f "$CADDYFILE" ]; then
    caddy_backup=$(mktemp "$(dirname "$CADDYFILE")/.giocondo-caddy-backup.XXXXXX")
    cp -p "$CADDYFILE" "$caddy_backup"
    chmod "$(stat -c '%a' "$CADDYFILE" 2>/dev/null || stat -f '%Lp' "$CADDYFILE")" "$tmp"
    chown "$(stat -c '%u:%g' "$CADDYFILE" 2>/dev/null || stat -f '%u:%g' "$CADDYFILE")" "$tmp"
  fi
  if replace_if_changed "$tmp" "$CADDYFILE"; then
    log "Caddy configured for https://$(caddy_host):8443"
    systemctl enable caddy >/dev/null 2>&1
    if ! systemctl reload-or-restart caddy; then
      if [ -n "$caddy_backup" ]; then mv "$caddy_backup" "$CADDYFILE"; else rm -f "$CADDYFILE"; fi
      systemctl reload-or-restart caddy >/dev/null 2>&1 || true
      die "Caddy reload failed; previous configuration restored"
    fi
    install -d -m 0700 "$INSTALL_STATE"
    file_sha "$CADDYFILE" >"$INSTALL_STATE/caddy.sha256"
    chmod 0600 "$INSTALL_STATE/caddy.sha256"
  elif ! systemctl is-active --quiet caddy; then
    systemctl start caddy
  fi
  [ -z "$caddy_backup" ] || rm -f "$caddy_backup"
}

file_sha() { openssl dgst -sha256 "$1" | awk '{print $NF}'; }

check_caddy_ownership() {
  [ ! -L "$CADDYFILE" ] || die "refusing to replace symlink $CADDYFILE"
  [ -e "$CADDYFILE" ] || return 0
  [ -f "$CADDYFILE" ] || die "$CADDYFILE is not a regular file"
  actual_sha=$(file_sha "$CADDYFILE")
  if [ -f "$INSTALL_STATE/caddy.sha256" ] && [ "$actual_sha" = "$(cat "$INSTALL_STATE/caddy.sha256")" ]; then return; fi
  packaged_md5=$(dpkg-query -W -f='${Conffiles}\n' caddy 2>/dev/null | awk -v p="$CADDYFILE" '$1==p {print $2}') || packaged_md5=
  actual_md5=$(openssl dgst -md5 "$CADDYFILE" | awk '{print $NF}')
  if [ -n "$packaged_md5" ] && [ "$actual_md5" = "$packaged_md5" ]; then return; fi
  die "$CADDYFILE is not an unchanged package default or installer-owned file. Keep it and integrate this site manually:
$(caddy_site)"
}

caddy_site() {
  admin_backend=$(read_env PANEL_LISTEN)
  sub_backend=$(read_env PANEL_SUB_LISTEN)
  admin_backend=${admin_backend:-127.0.0.1:8080}
  sub_backend=${sub_backend:-127.0.0.1:8081}
  case "$admin_backend" in :*|0.0.0.0:*|'[::]':*) admin_backend=127.0.0.1:${admin_backend##*:} ;; esac
  case "$sub_backend" in :*|0.0.0.0:*|'[::]':*) sub_backend=127.0.0.1:${sub_backend##*:} ;; esac
  # Clients connecting by IP send no SNI, so Caddy would pick the certificate by the local address:
  # that breaks the loopback check in wait_tls and clients behind provider NAT.
  default_sni=
  if [ "$MODE" = ip ]; then default_sni="
	default_sni $IP"; fi
  cat <<EOF
{
	servers {
		protocols h1 h2
	}$default_sni
}
EOF
  if [ "$MODE" = ip ]; then
    # Let's Encrypt issues IP certificates only with the short-lived profile; ZeroSSL does not issue them.
    # TLS-ALPN would need port 443, which belongs to VLESS Reality. The admin UI stays behind the SSH tunnel.
    cat <<EOF
https://$IP:8443 {
	tls {
		issuer acme {
			profile shortlived
			disable_tlsalpn_challenge
		}
	}
	@subscriptions path /sub/* /health/*
	handle @subscriptions {
		reverse_proxy $sub_backend
	}
	handle {
		respond 404
	}
}
EOF
    return
  fi
  cat <<EOF
https://$DOMAIN:8443 {
	@subscriptions path /sub/* /health/*
	reverse_proxy @subscriptions $sub_backend
	reverse_proxy $admin_backend
}
EOF
}

caddy_host() { if [ "$MODE" = ip ]; then echo "$IP"; else echo "$DOMAIN"; fi; }

start_panel() {
  systemctl enable giocondo-vpn-panel >/dev/null 2>&1
  if [ "$PANEL_CHANGED" = 1 ] || ! systemctl is-active --quiet giocondo-vpn-panel; then
    log "starting the panel"
    systemctl restart giocondo-vpn-panel
  fi
  i=0
  until systemctl is-active --quiet giocondo-vpn-panel; do
    i=$((i + 1))
    [ "$i" -le 15 ] || die "the panel did not start: journalctl -u giocondo-vpn-panel"
    sleep 1
  done
}

setup_node() {
  agent_dir=$(dirname "$AGENT_CERT")
  if [ -e "$AGENT_CERT" ] || [ -e "$agent_dir/agent.key" ] || [ -e "$agent_dir/ca.crt" ]; then
    [ -s "$AGENT_CERT" ] && [ -s "$agent_dir/agent.key" ] && [ -s "$agent_dir/ca.crt" ] || die "partial agent credentials; repair them explicitly"
    openssl verify -purpose sslclient -CAfile "$agent_dir/ca.crt" "$AGENT_CERT" >/dev/null 2>&1 || die "invalid or expired agent certificate"
    openssl verify -purpose sslclient -CAfile "$PKI/ca.crt" "$AGENT_CERT" >/dev/null 2>&1 || die "agent belongs to another panel CA"
    key_pub=$(openssl pkey -in "$agent_dir/agent.key" -pubout 2>/dev/null) || die "invalid agent private key"
    cert_pub=$(openssl x509 -in "$AGENT_CERT" -pubkey -noout 2>/dev/null) || die "invalid agent certificate"
    [ "$key_pub" = "$cert_pub" ] || die "agent certificate and key do not match"
    systemctl enable giocondo-vpn-agent giocondo-vpn-sing-box >/dev/null 2>&1
    systemctl is-active --quiet giocondo-vpn-agent || systemctl start giocondo-vpn-agent
    if [ -f /etc/giocondo-vpn/sing-box/config.json ]; then
      systemctl is-active --quiet giocondo-vpn-sing-box || systemctl start giocondo-vpn-sing-box
    fi
    return
  fi
  log "creating the local node"
  grpc_listen=$(read_env PANEL_GRPC_LISTEN)
  giocondo-vpn-panel local-node -host "${DOMAIN:-$IP}" -db "$STATE/panel.db" -pki "$PKI" -grpc "${grpc_listen:-:9443}"
}

node_ports() {
  if [ -f "$AGENT_CERT" ]; then
    giocondo-vpn-panel local-node -ports -db "$STATE/panel.db" -agent-cert "$AGENT_CERT"
  else
    echo '443/tcp 8443/udp 51820/udp'
  fi
}

open_firewall() {
  command -v ufw >/dev/null 2>&1 || return 0
  ufw status 2>/dev/null | grep -q '^Status: active' || return 0
  log "opening ports in ufw"
  rules=$(node_ports)
  if uses_caddy; then
    rules="$rules 80/tcp 8443/tcp"
  else
    sub_listen=$(read_env PANEL_SUB_LISTEN)
    case "$sub_listen" in 127.*|localhost:*|'[::1]'*) ;; *) rules="$rules ${sub_listen##*:}/tcp" ;; esac
  fi
  for r in $rules; do ufw allow "$r" >/dev/null; done
}

summary() {
  echo
  echo "giocondo-vpn is installed."
  public_url=$(read_env PANEL_PUBLIC_URL)
  case "$public_url" in http://127.*|http://localhost:*)
    admin_listen=$(read_env PANEL_LISTEN)
    admin_port=${admin_listen##*:}
    echo "  Admin UI:       through an SSH tunnel from your computer:"
    echo "                    ssh -L $admin_port:127.0.0.1:$admin_port root@$IP"
    echo "                  then open $public_url<PANEL_ADMIN_PATH>/" ;;
    *) echo "  Admin UI:       $public_url<PANEL_ADMIN_PATH>/" ;;
  esac
  echo "  Subscriptions:  $(read_env PANEL_SUB_URL)/sub/<token>"
  if [ "$MODE" = ip ]; then
    echo "                  the IP certificate lives about 6 days: keep port 80/tcp open for its renewal"
  fi
  echo "  Admin path and password:  grep -E '^PANEL_ADMIN_(PATH|PASSWORD)=' $ENV_FILE"
  echo "  Node ports:     $(node_ports)"
  echo "  Status:         run this script with: status"
}

backup_install() {
  install -d -m 0700 "$INSTALL_STATE"
  TX_DIR=$(mktemp -d "$INSTALL_STATE/transaction.XXXXXX")
  for entry in env cert key caddy checksum ip; do
    case "$entry" in
      env) source_path=$ENV_FILE ;; cert) source_path=$PKI/server.crt ;; key) source_path=$PKI/server.key ;;
      caddy) source_path=$CADDYFILE ;; checksum) source_path=$INSTALL_STATE/caddy.sha256 ;; ip) source_path=$INSTALL_STATE/ip ;;
    esac
    if [ -f "$source_path" ]; then cp -p "$source_path" "$TX_DIR/$entry"; else : >"$TX_DIR/$entry.missing"; fi
  done
  CADDY_WAS_ACTIVE=0
  if systemctl is-active --quiet caddy; then CADDY_WAS_ACTIVE=1; fi
  trap 'install_exit $?' 0
}

restore_file() {
  if [ -f "$TX_DIR/$2.missing" ]; then
    rm -f "$1"
  else
    restore_tmp=$(mktemp "$(dirname "$1")/.giocondo-restore.XXXXXX") || return 1
    cp -p "$TX_DIR/$2" "$restore_tmp" || return 1
    mv "$restore_tmp" "$1" || return 1
  fi
}

install_exit() {
  install_rc=$1
  trap - 0
  if [ "$install_rc" != 0 ]; then
    restore_failed=0
    agent_dir=$(dirname "$AGENT_CERT")
    if [ "$EXISTING" = 0 ] && [ -s "$AGENT_CERT" ] && [ -s "$agent_dir/agent.key" ] && [ -s "$agent_dir/ca.crt" ] && [ -s "$PKI/server.crt" ]; then
      warn "initial node credentials installed; keeping panel settings and certificates for retry"
    else
      warn "installation failed; restoring previous panel settings and certificates"
      restore_file "$ENV_FILE" env || restore_failed=1
      restore_file "$PKI/server.crt" cert || restore_failed=1
      restore_file "$PKI/server.key" key || restore_failed=1
    fi
    restore_file "$CADDYFILE" caddy || restore_failed=1
    restore_file "$INSTALL_STATE/caddy.sha256" checksum || restore_failed=1
    restore_file "$INSTALL_STATE/ip" ip || restore_failed=1
    if [ "$CADDY_WAS_ACTIVE" = 1 ]; then systemctl reload-or-restart caddy || true; else systemctl stop caddy >/dev/null 2>&1 || true; fi
    systemctl restart giocondo-vpn-panel >/dev/null 2>&1 || true
    if [ "$restore_failed" = 1 ]; then
      warn "restoration incomplete; backups retained at $TX_DIR"
      exit "$install_rc"
    fi
  fi
  rm -rf "$TX_DIR"
  exit "$install_rc"
}

wait_ready() {
  log "waiting for the local node and applied configuration"
  if ! giocondo-vpn-panel local-node -check -db "$STATE/panel.db" -agent-cert "$AGENT_CERT" -timeout 120s; then
    die "VPN is not ready: journalctl -u giocondo-vpn-panel -u giocondo-vpn-agent -u giocondo-vpn-sing-box"
  fi
  /usr/lib/giocondo-vpn/sing-box check -c /etc/giocondo-vpn/sing-box/config.json
  for s in giocondo-vpn-panel giocondo-vpn-agent giocondo-vpn-sing-box; do
    systemctl is-active --quiet "$s" || die "$s is not active: journalctl -u $s"
  done
  if uses_caddy; then systemctl is-active --quiet caddy || die "Caddy is not active"; fi
  if [ "$MODE" = ip ]; then wait_tls; fi
}

# wait_tls checks that Caddy obtained the IP certificate and proxies subscriptions. --connect-to avoids
# hairpin NAT while curl still verifies the certificate against the public IP with the system CAs.
wait_tls() {
  log "waiting for the Let's Encrypt certificate for $IP"
  challenge=$(openssl rand -hex 32)
  i=0
  until [ "$(curl -fsS --max-time 5 --connect-to "$IP:8443:127.0.0.1:8443" \
    "https://$IP:8443/health/challenge?challenge=$challenge" 2>/dev/null)" = "$challenge" ]; do
    i=$((i + 1))
    [ "$i" -le 24 ] || die "no valid HTTPS certificate for $IP: port 80/tcp must be reachable from the internet (journalctl -u caddy); to install without HTTPS, run again with --http"
    sleep 5
  done
}

cmd_install() {
  check_system
  prepare_config
  case "$IP" in 10.*|192.168.*|172.1[6-9].*|172.2[0-9].*|172.3[01].*)
    warn "$IP is a private address; if clients reach the server by another IP, run again with --ip" ;;
  esac
  case "$MODE" in
    domain) log "mode: domain $DOMAIN (server IP $IP)" ;;
    ip) log "mode: HTTPS by IP $IP" ;;
    *) log "mode: HTTP by IP $IP" ;;
  esac
  PANEL_CHANGED=0
  setup_repository
  install_packages
  help_output=$(giocondo-vpn-panel local-node -h 2>&1 || true)
  printf '%s\n' "$help_output" | grep -q -- '-check' || die "installed panel version does not support local-node -check; choose a compatible version"
  if uses_caddy; then
    install_caddy
    check_caddy_ownership
  fi
  backup_install
  configure_panel
  issue_panel_cert
  open_firewall
  if uses_caddy; then configure_caddy; fi
  if [ ! -e "$AGENT_CERT" ]; then
    # The first local-node command migrates the database. Do not race the panel's startup migrations.
    systemctl stop giocondo-vpn-panel
    setup_node
    start_panel
  else
    start_panel
    setup_node
  fi
  wait_ready
  printf '%s\n' "$IP" >"$INSTALL_STATE/ip"
  chmod 0600 "$INSTALL_STATE/ip"
  summary
}

cmd_status() {
  for s in giocondo-vpn-panel giocondo-vpn-agent giocondo-vpn-sing-box; do
    printf '%-24s %s\n' "$s" "$(systemctl is-active "$s" 2>/dev/null || true)"
  done
  if [ -r "$ENV_FILE" ]; then
    grep -E '^PANEL_(PUBLIC_URL|SUB_URL)=' "$ENV_FILE" || true
  fi
}

cmd_uninstall() {
  [ "$(id -u)" -eq 0 ] || die "run as root"
  export DEBIAN_FRONTEND=noninteractive
  if [ "$PURGE" = 1 ]; then
    confirm "Remove giocondo-vpn packages and their settings?" || exit 1
    # shellcheck disable=SC2086 # list of packages
    apt-get purge -y -qq $PACKAGES
    rm -f "$SOURCES" "$KEYRING"
    if [ -d "$STATE" ] && confirm "Also delete $STATE (users, nodes, the node CA)? This cannot be undone."; then
      rm -rf "$STATE"
    fi
  else
    confirm "Remove giocondo-vpn packages (settings and data are kept)?" || exit 1
    # shellcheck disable=SC2086 # list of packages
    apt-get remove -y -qq $PACKAGES
  fi
  log "removed"
}

main() {
  COMMAND=install DOMAIN='' IP='' MODE='' PKG_VERSION='' YES=0 PURGE=0 RECONFIGURE=0 DOMAIN_SET=0 IP_SET=0 HTTP_SET=0
  while [ $# -gt 0 ]; do
    case "$1" in
      install|status|uninstall) COMMAND=$1; shift ;;
      --domain) DOMAIN=${2:?--domain needs a value}; DOMAIN_SET=1; shift 2 ;;
      --ip) IP=${2:?--ip needs a value}; IP_SET=1; shift 2 ;;
      --http) HTTP_SET=1; shift ;;
      --reconfigure) RECONFIGURE=1; shift ;;
      --version) PKG_VERSION=${2:?--version needs a value}; shift 2 ;;
      --repo) REPO=${2%/}; shift 2 ;;
      --yes|-y) YES=1; shift ;;
      --purge) PURGE=1; shift ;;
      -h|--help) usage 0 ;;
      *) echo "unknown argument: $1" >&2; usage 1 ;;
    esac
  done
  case "$COMMAND" in
    install) cmd_install ;;
    status) cmd_status ;;
    uninstall) cmd_uninstall ;;
  esac
}

main "$@"
