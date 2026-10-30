#!/bin/sh
# This hook is shipped inside every incoming deb: it must not depend on installed project code.
set -eu
umask 077

fail() { echo "giocondo-vpn upgrade: $*" >&2; exit 1; }
BACKUPS=/var/lib/giocondo-vpn/backups
PACKAGES='giocondo-vpn-panel giocondo-vpn-agent giocondo-vpn-sing-box'

# dpkg passes the old and incoming versions to preinst on Debian 11+.
case "${1:-}" in install|upgrade) ;; *) exit 0 ;; esac
old=${2:-} new=${3:-}
[ -n "$old" ] || exit 0
[ -n "$new" ] || fail "incoming version is missing"
dpkg --compare-versions "$old" eq "$new" && exit 0

if [ -n "${GIOCONDO_RESTORE_FROM:-}" ]; then
  # Recovery only permits the exact versions recorded in a complete, verified snapshot.
  recovery=$(realpath -e "$GIOCONDO_RESTORE_FROM")
  [ -f "$recovery/SHA256SUMS" ] || fail "incomplete recovery snapshot"
  (cd "$recovery" && sha256sum -c SHA256SUMS >/dev/null) || fail "recovery checksum failure"
  expected=$(awk -F '\t' -v p="${DPKG_MAINTSCRIPT_PACKAGE:?}" '$1 == p {print $2}' "$recovery/versions.tsv")
  [ "$new" = "$expected" ] || fail "recovery package does not match snapshot"
  exit 0
fi

dpkg --compare-versions "$new" lt "$old" && fail "downgrade refused; restore matching packages and data using the recovery procedure"
branch() {
  # Strip Debian epoch, revision and prerelease suffix, then identify the compatibility branch.
  value=${1#*:}; value=${value%%-*}; value=${value%%~*}
  printf '%s\n' "$value" | awk -F. '
    NF != 3 || $1 !~ /^[0-9]+$/ || $2 !~ /^[0-9]+$/ || $3 !~ /^[0-9]+$/ {exit 1}
    {if ($1 == 0) print $1 "." $2; else print $1}'
}
old_branch=$(branch "$old") || fail "unrecognized installed version"
new_branch=$(branch "$new") || fail "unrecognized incoming version"
if [ "$old_branch" != "$new_branch" ] && [ "${GIOCONDO_ALLOW_BREAKING_UPGRADE:-0}" != 1 ]; then
  fail "breaking upgrade requires GIOCONDO_ALLOW_BREAKING_UPGRADE=1 (installer: --allow-breaking-upgrade)"
fi

# EnvironmentFile is not a shell script. Accept literal paths without executing expansions.
env_path() {
  file=$1 key=$2 default=$3
  value=
  if [ -f "$file" ]; then
    value=$(awk -v k="$key" '
      /^[[:space:]]*#/ {next}
      {line=$0; sub(/^[[:space:]]*/, "", line); pos=index(line,"=")
       if (pos && substr(line,1,pos-1)==k) v=substr(line,pos+1)}
      END {print v}' "$file")
  fi
  # A systemd drop-in may override EnvironmentFile. Prefer the running process's value.
  case "$key" in PANEL_*) service=giocondo-vpn-panel ;; *) service=giocondo-vpn-agent ;; esac
  pid=$(systemctl show -p MainPID --value "$service" 2>/dev/null || true)
  case "$pid" in ''|*[!0-9]*) ;; *)
    if [ -r "/proc/$pid/environ" ]; then
      effective=$(tr '\000' '\n' < "/proc/$pid/environ" | awk -v k="$key" 'index($0,k "=")==1 {print substr($0,length(k)+2)}')
      [ -z "$effective" ] || value=$effective
    fi ;;
  esac
  value=$(printf '%s' "$value" | sed 's/^[[:space:]]*//;s/[[:space:]]*$//')
  case "$value" in \"*\") value=${value#\"}; value=${value%\"} ;; \'*\') value=${value#\'}; value=${value%\'} ;; esac
  [ -n "$value" ] || value=$default
  case "$value" in *\$*|*\`*|*\\*|*'
'*) fail "unsupported expansion in $key; use an absolute literal path" ;; esac
  case "$value" in /*) ;; *) fail "$key must be an absolute literal path" ;; esac
  printf '%s\n' "$value"
}

panel_db=$(env_path /etc/giocondo-vpn/panel.env PANEL_DB /var/lib/giocondo-vpn/panel/panel.db)
canonical_db=$(realpath -m "$panel_db")
panel_pki=$(env_path /etc/giocondo-vpn/panel.env PANEL_PKI_DIR /var/lib/giocondo-vpn/panel/pki)
agent_state=$(env_path /etc/giocondo-vpn/agent.env AGENT_STATE_DIR /var/lib/giocondo-vpn/agent)
agent_config=$(env_path /etc/giocondo-vpn/agent.env AGENT_SINGBOX_CONFIG /etc/giocondo-vpn/sing-box/config.json)
agent_ca=$(env_path /etc/giocondo-vpn/agent.env AGENT_CA /etc/giocondo-vpn/agent/ca.crt)
agent_cert=$(env_path /etc/giocondo-vpn/agent.env AGENT_CERT /etc/giocondo-vpn/agent/agent.crt)
agent_key=$(env_path /etc/giocondo-vpn/agent.env AGENT_KEY /etc/giocondo-vpn/agent/agent.key)

mkdir -p "$BACKUPS"
chmod 0700 "$BACKUPS"
snapshot=$(mktemp -d "$BACKUPS/upgrade.XXXXXXXX")
active=$snapshot/active-services
: > "$active"
resume() {
  result=$?
  trap - EXIT HUP INT TERM
  while IFS= read -r service; do
    if ! systemctl start "$service"; then
      echo "giocondo-vpn upgrade: could not resume $service; snapshot: $snapshot" >&2
      result=1
    fi
  done < "$active"
  [ "$result" -eq 0 ] || echo "giocondo-vpn upgrade failed; retained snapshot files: $snapshot" >&2
  exit "$result"
}
trap resume EXIT
trap 'exit 1' HUP INT TERM
if [ -d /run/systemd/system ]; then
  # Record all running services before stopping any, including the local TLS certificate writer.
  for service in giocondo-vpn-agent giocondo-vpn-panel giocondo-vpn-sing-box caddy; do
    if systemctl is-active --quiet "$service"; then echo "$service" >> "$active"; fi
  done
  while IFS= read -r service; do
    systemctl stop "$service" || fail "could not stop $service"
    if systemctl is-active --quiet "$service"; then fail "$service is still running"; fi
  done < "$active"
fi

: > "$snapshot/paths"
add_path() {
  [ -e "$1" ] || [ -L "$1" ] || return 0
  resolved=$(realpath -e "$1") || fail "unreadable snapshot path"
  case "$BACKUPS/" in "$resolved/"*) fail "a data path contains the backup directory" ;; esac
  # Keep both the symlink and its target, so custom storage can be restored in place.
  printf '%s\n' "${1#/}" "${resolved#/}" >> "$snapshot/paths"
}
for path in /etc/giocondo-vpn /var/lib/giocondo-vpn/panel /var/lib/giocondo-vpn/agent \
  /var/lib/giocondo-vpn/sing-box /var/lib/giocondo-vpn/installer /etc/caddy /var/lib/caddy \
  "$panel_db" "$canonical_db" "$canonical_db-wal" "$canonical_db-shm" "$panel_pki" "$agent_state" \
  "$agent_config" "$agent_config.prev" "$agent_ca" "$agent_cert" "$agent_key"; do
  add_path "$path"
done
for service in giocondo-vpn-panel giocondo-vpn-agent giocondo-vpn-sing-box caddy; do
  add_path "/etc/systemd/system/$service.service"
  add_path "/etc/systemd/system/$service.service.d"
done
sort -u "$snapshot/paths" -o "$snapshot/paths"
# tar treats a second appearance of the same inode as a hardlink, even when the
# name is identical. Only archive the outermost paths; directory traversal saves children.
mv "$snapshot/paths" "$snapshot/all-paths"
: > "$snapshot/paths"
while IFS= read -r path; do
  covered=0
  while IFS= read -r parent; do
    case "$path/" in "$parent/"*) covered=1; break ;; esac
  done < "$snapshot/paths"
  [ "$covered" = 1 ] || printf '%s\n' "$path" >> "$snapshot/paths"
done < "$snapshot/all-paths"
rm -f "$snapshot/all-paths"
tar -C / --verbatim-files-from -cpf "$snapshot/state.tar" -T "$snapshot/paths" || fail "snapshot archive failed"
: > "$snapshot/versions.tsv"
: > "$snapshot/package-status.tsv"
for package in $PACKAGES; do
  info=$(dpkg-query -W -f='${db:Status-Status}\t${Version}' "$package" 2>/dev/null || true)
  state=$(printf '%s' "$info" | cut -f1)
  version=$(printf '%s' "$info" | cut -f2)
  # dpkg marks the package being upgraded half-installed before invoking preinst.
  # Its old files are still intact here; the old-version argument is authoritative.
  if [ "$package" = "$DPKG_MAINTSCRIPT_PACKAGE" ] && [ "$state" = half-installed ]; then
    state=installed
    version=$old
  fi
  case "$state" in
    installed|unpacked|half-configured|triggers-awaited|triggers-pending)
      printf '%s\t%s\n' "$package" "$version" >> "$snapshot/versions.tsv" ;;
    '') state=absent; version=- ;;
    config-files|not-installed) version=- ;;
    *) fail "cannot snapshot incomplete package state for $package" ;;
  esac
  printf '%s\t%s\t%s\n' "$package" "$state" "$version" >> "$snapshot/package-status.tsv"
done
[ -s "$snapshot/versions.tsv" ] || fail "cannot record installed package versions"
printf '%s\n' "$canonical_db" > "$snapshot/database-path"
printf '%s\t%s\t%s\n' "${DPKG_MAINTSCRIPT_PACKAGE:?}" "$old" "$new" > "$snapshot/transition.tsv"
(cd "$snapshot" && sha256sum state.tar paths versions.tsv package-status.tsv active-services transition.tsv database-path > SHA256SUMS.new && sha256sum -c SHA256SUMS.new >/dev/null && mv SHA256SUMS.new SHA256SUMS)
echo "giocondo-vpn upgrade: verified recovery snapshot saved at $snapshot"
