#!/bin/sh
# Separate client-format compatibility from the server core version.
set -eu
cd "$(dirname "$0")/.."
case "$(uname -s)" in Linux) platform=linux ;; Darwin) platform=darwin ;; *) exit 1 ;; esac
case "$(uname -m)" in x86_64|amd64) arch=amd64 ;; arm64|aarch64) arch=arm64 ;; *) exit 1 ;; esac
tools=$PWD/bin/client-validators
mkdir -p "$tools/floor"
if [ ! -x "$tools/floor/sing-box" ]; then
  curl -fL --retry 3 "https://github.com/SagerNet/sing-box/releases/download/v1.12.0/sing-box-1.12.0-$platform-$arch.tar.gz" -o "$tools/floor.tar.gz"
  tar -xzf "$tools/floor.tar.gz" -C "$tools/floor" --strip-components=1
fi
if [ ! -x "$tools/mihomo" ]; then
  curl -fL --retry 3 "https://github.com/MetaCubeX/mihomo/releases/download/v1.19.0/mihomo-$platform-$arch-v1.19.0.gz" -o "$tools/mihomo.gz"
  gzip -dc "$tools/mihomo.gz" > "$tools/mihomo.new"
  chmod +x "$tools/mihomo.new"
  mv "$tools/mihomo.new" "$tools/mihomo"
fi
if [ "$platform" = linux ]; then
  PATH="$tools:$PWD/bin/network-e2e:$PWD/bin:$PATH"
else
  PATH="$tools:$PWD/bin:$PATH"
fi
export PATH
command -v sing-box >/dev/null
export SINGBOX_EXTRA="$tools/floor/sing-box" MIHOMO_CHECK=1
go test -count=1 ./internal/singbox ./internal/subscription
