#!/bin/sh
# Explicit operator recovery only. Never called automatically by install or package hooks.
set -eu
[ "$(id -u)" -eq 0 ] || { echo 'run recovery as root' >&2; exit 1; }
action=${1:-} snapshot=${2:-}
case "$action" in verify|restore-state) ;; *) echo "usage: $0 verify|restore-state SNAPSHOT" >&2; exit 1 ;; esac
snapshot=$(realpath -e "$snapshot")
[ -f "$snapshot/SHA256SUMS" ] || { echo 'incomplete snapshot' >&2; exit 1; }
(cd "$snapshot" && sha256sum -c SHA256SUMS >/dev/null)
[ "$action" = restore-state ] || { echo 'snapshot checksums verified'; exit 0; }

for service in giocondo-vpn-panel giocondo-vpn-agent giocondo-vpn-sing-box caddy; do
  if systemctl is-active --quiet "$service"; then
    echo "stop $service before restoring state" >&2
    exit 1
  fi
done
tab=$(printf '\t')
while IFS="$tab" read -r package state version; do
  current=$(dpkg-query -W -f='${db:Status-Status}' "$package" 2>/dev/null || true)
  case "$state" in absent|config-files|not-installed)
    case "$current" in ''|config-files|not-installed) ;; *)
      echo "remove newly introduced package $package (without purge) before restoring state" >&2
      exit 1 ;;
    esac ;;
  *)
    case "$current" in installed|unpacked|half-configured|triggers-awaited|triggers-pending) ;; *)
      echo "unpack the recorded package $package before restoring state" >&2
      exit 1 ;;
    esac ;;
  esac
done < "$snapshot/package-status.tsv"
while IFS="$tab" read -r package version; do
  current=$(dpkg-query -W -f='${Version}' "$package")
  [ "$current" = "$version" ] || { echo "restore the recorded version of $package before restoring state" >&2; exit 1; }
done < "$snapshot/versions.tsv"
# The database may have acquired WAL files after the snapshot. Remove only these
# companions before restoring, including when they were absent from the snapshot.
if [ -f "$snapshot/database-path" ]; then
  database=$(cat "$snapshot/database-path")
  case "$database" in /*) ;; *) echo 'invalid database path' >&2; exit 1 ;; esac
  rm -f -- "$database-wal" "$database-shm"
fi
# Restore complete saved directories, not a merge with files created by the failed release.
tar --recursive-unlink -C / -xpf "$snapshot/state.tar"
echo "state restored; start the services listed in $snapshot/active-services and run readiness checks"
