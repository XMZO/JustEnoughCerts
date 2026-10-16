#!/usr/bin/env bash
# Prints a Markdown table with the size of each binary and archive of a
# GoReleaser build and, given a pushed image, of each platform's image as
# pulled: the sum of its compressed layers.
#
#   scripts/release-sizes.sh [image@sha256:digest]
#
# DIST overrides the GoReleaser output directory (default dist).
set -euo pipefail

dist=${DIST:-dist}
image=${1:-}

human() {
  awk -v b="$1" 'BEGIN {
    split("B KiB MiB GiB", unit); i = 1
    while (b >= 1024 && i < 4) { b /= 1024; i++ }
    printf(i == 1 ? "%d %s" : "%.1f %s", b, unit[i])
  }'
}

# Collect everything first: a failing command inside "< <(...)" would go
# unnoticed, while a failing "$(...)" assignment stops the script.
artifacts=$(jq -r '.[] | select(.type == "Binary" or .type == "Archive")
  | [.type, .goos + "/" + .goarch, .path] | @tsv' "$dist/artifacts.json")
if [ -z "$artifacts" ]; then
  echo "no binaries in $dist/artifacts.json" >&2
  exit 1
fi

declare -A binary archive pulled
while IFS=$'\t' read -r type platform path; do
  size=$(wc -c <"$path")
  case $type in
    Binary) binary[$platform]=$size ;;
    Archive) archive[$platform]=$size ;;
  esac
done <<<"$artifacts"

if [ -n "$image" ]; then
  repo=${image%@*}
  index=$(docker buildx imagetools inspect --raw "$image")
  platforms=$(jq -r '.manifests[] | select(.platform.os != "unknown")
    | [.platform.os + "/" + .platform.architecture, .digest] | @tsv' <<<"$index")
  while IFS=$'\t' read -r platform digest; do
    manifest=$(docker buildx imagetools inspect --raw "$repo@$digest")
    pulled[$platform]=$(jq '[.layers[].size] | add' <<<"$manifest")
  done <<<"$platforms"
  echo "| Platform | Binary | Archive | Image |"
  echo "|---|---:|---:|---:|"
else
  echo "| Platform | Binary | Archive |"
  echo "|---|---:|---:|"
fi

for platform in $(printf '%s\n' "${!binary[@]}" | sort); do
  row="| $platform | $(human "${binary[$platform]}") | $(human "${archive[$platform]:-0}") |"
  if [ -n "$image" ]; then
    if [ -n "${pulled[$platform]:-}" ]; then
      row+=" $(human "${pulled[$platform]}") |"
    else
      row+=" - |"
    fi
  fi
  echo "$row"
done
