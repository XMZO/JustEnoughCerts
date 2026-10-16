#!/usr/bin/env bash
# Copies the linux binaries of a GoReleaser build into the layout that the
# Dockerfile "release" target expects: <context>/<os>/<arch>/jec.
#
#   scripts/stage-image-context.sh [dist-dir] [context-dir]
set -euo pipefail

dist=${1:-dist}
context=${2:-build/image}

binaries=$(jq -r '.[] | select(.type == "Binary" and .goos == "linux")
  | [.goos, .goarch, .path] | @tsv' "$dist/artifacts.json")
if [ -z "$binaries" ]; then
  echo "no linux binaries in $dist/artifacts.json" >&2
  exit 1
fi

rm -rf "$context"
while IFS=$'\t' read -r os arch path; do
  install -D -m 0755 "$path" "$context/$os/$arch/jec"
  echo "$context/$os/$arch/jec"
done <<<"$binaries"
