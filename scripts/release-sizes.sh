#!/usr/bin/env bash
# Prints a Markdown table with the size of each binary and archive of a
# GoReleaser build and, given a pushed image, of each platform's image as
# pulled: the sum of its compressed layers. Fails when one of them is over
# its budget. With --packages, it also lists the biggest packages of the
# linux/amd64 binary, as go-size-analyzer measures them.
#
#   scripts/release-sizes.sh [--packages] [image@sha256:digest]
#
# DIST overrides the GoReleaser output directory (default dist).
set -euo pipefail

# The budgets of PLAN.md 8.3. Raise one only in the change that needs it,
# and say there why the growth is worth it. An image takes the archive's
# budget, since it holds little more than the binary.
binary_budget=$((88 * 1024 * 1024))
archive_budget=$((30 * 1024 * 1024))
gsa_version=v1.14.1

dist=${DIST:-dist}
packages=false
if [ "${1:-}" = --packages ]; then
  packages=true
  shift
fi
image=${1:-}

human() {
  awk -v b="$1" 'BEGIN {
    split("B KiB MiB GiB", unit); i = 1
    while (b >= 1024 && i < 4) { b /= 1024; i++ }
    printf(i == 1 ? "%d %s" : "%.1f %s", b, unit[i])
  }'
}

over=()
check() { # what size budget
  if [ "$2" -gt "$3" ]; then
    over+=("$1 is $(human "$2"), over its budget of $(human "$3")")
  fi
}

# Collect everything first: a failing command inside "< <(...)" would go
# unnoticed, while a failing "$(...)" assignment stops the script.
# Platforms are named as Docker names them: linux/arm/v7 for 32-bit ARM.
artifacts=$(jq -r '.[] | select(.type == "Binary" or .type == "Archive")
  | [.type, .goos + "/" + .goarch + (if (.goarm // "") != "" then "/v" + .goarm else "" end), .path] | @tsv' "$dist/artifacts.json")
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
    | [.platform.os + "/" + .platform.architecture + (if .platform.architecture == "arm" then "/" + .platform.variant else "" end), .digest] | @tsv' <<<"$index")
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
  check "the $platform binary" "${binary[$platform]}" "$binary_budget"
  check "the $platform archive" "${archive[$platform]:-0}" "$archive_budget"
  row="| $platform | $(human "${binary[$platform]}") | $(human "${archive[$platform]:-0}") |"
  if [ -n "$image" ]; then
    if [ -n "${pulled[$platform]:-}" ]; then
      check "the $platform image" "${pulled[$platform]}" "$archive_budget"
      row+=" $(human "${pulled[$platform]}") |"
    else
      row+=" - |"
    fi
  fi
  echo "$row"
done
echo
echo "Budgets: $(human "$binary_budget") for a binary, $(human "$archive_budget") for an archive or an image."

if $packages; then
  bin=$(jq -r '.[] | select(.type == "Binary" and .goos == "linux" and .goarch == "amd64") | .path' "$dist/artifacts.json")
  report=$(go run "github.com/Zxilly/go-size-analyzer/cmd/gsa@$gsa_version" -f json "$bin")
  rows=$(jq -r '.size as $total | [.packages[] | {name, size}] | sort_by(-.size) | .[:15][]
    | [.name, .size, 100 * .size / $total] | @tsv' <<<"$report")
  echo
  echo "The biggest packages of the linux/amd64 binary:"
  echo
  echo "| Package | Size | Share |"
  echo "|---|---:|---:|"
  while IFS=$'\t' read -r name size share; do
    printf '| %s | %s | %.1f%% |\n' "$name" "$(human "$size")" "$share"
  done <<<"$rows"
fi

if [ ${#over[@]} -gt 0 ]; then
  for msg in "${over[@]}"; do
    if [ "${GITHUB_ACTIONS:-}" = true ]; then
      echo "::error title=Size budget::$msg" >&2
    else
      echo "$msg" >&2
    fi
  done
  exit 1
fi
