#!/bin/sh
# Converts the runetfreedom sing-box rule sets to Mihomo mrs files in OUT_DIR, which must not exist.
# Clients on Mihomo download them as rule-providers: FlClash and Clash Verge Rev replace geodata from a
# profile with their own files, which lack ru-blocked. The result is all or nothing, so a failed build
# cannot publish part of the lists or lists that lost rules.
#
# Usage: build.sh OUT_DIR
# Environment: SING_BOX and MIHOMO are the tools (default: from PATH), RULES_BASE overrides the source.
set -eu

[ $# -eq 1 ] || { echo "usage: $0 OUT_DIR" >&2; exit 64; }

out=$1
sing_box=${SING_BOX:-sing-box}
mihomo=${MIHOMO:-mihomo}
base=${RULES_BASE:-https://raw.githubusercontent.com/runetfreedom/russia-v2ray-rules-dat/release}

[ ! -e "$out" ] || { echo "build.sh: $out already exists" >&2; exit 1; }

mkdir -p "$(dirname "$out")"
work=$(mktemp -d "$out.XXXXXX")
trap 'rm -rf "$work"' EXIT

# Only these rule types exist in the Mihomo domain and ipcidr formats. Anything else fails the build
# instead of being dropped, because dropping it would silently change routing.
to_text='
  def allowed: if $behavior == "domain" then ["domain", "domain_suffix"] else ["ip_cidr"] end;
  .rules[] | to_entries[]
  | .key as $kind
  | if any(allowed[]; . == $kind) then . else error("unexpected rule type \($kind) in a \($behavior) set") end
  | .value | (if type == "array" then .[] else . end)
  | if $kind == "domain_suffix" then "+." + ltrimstr(".") else . end
'

convert() {
  behavior=$1
  path=$2
  tag=${path##*/}

  curl -fsSL --retry 3 --max-time 120 -o "$work/$tag.srs" "$base/$path.srs" ||
    { echo "build.sh: $tag: download failed" >&2; exit 1; }
  "$sing_box" rule-set decompile -o "$work/$tag.json" "$work/$tag.srs" ||
    { echo "build.sh: $tag: decompile failed" >&2; exit 1; }
  jq -r --arg behavior "$behavior" "$to_text" "$work/$tag.json" > "$work/$tag.txt" ||
    { echo "build.sh: $tag: conversion failed" >&2; exit 1; }
  [ -s "$work/$tag.txt" ] || { echo "build.sh: $tag: no rules" >&2; exit 1; }
  "$mihomo" convert-ruleset "$behavior" text "$work/$tag.txt" "$work/$tag.mrs" ||
    { echo "build.sh: $tag: convert-ruleset failed" >&2; exit 1; }
  rm "$work/$tag.srs" "$work/$tag.json" "$work/$tag.txt"
  echo "$tag.mrs"
}

# Keep in sync with allRuleSets in internal/service/subscription/routing.go (checked by a test there).
convert domain sing-box/rule-set-geosite/geosite-ru-blocked
convert ipcidr sing-box/rule-set-geoip/geoip-ru-blocked
convert domain sing-box/rule-set-geosite/geosite-category-ru
convert domain sing-box/rule-set-geosite/geosite-ru-available-only-inside
convert ipcidr sing-box/rule-set-geoip/geoip-ru

mv "$work" "$out"
trap - EXIT
