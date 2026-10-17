#!/usr/bin/env bash
# Checks the module rules of PLAN.md 4.2 that take a build, in a copy of the
# repository so that the working tree is not touched: a scaffolded module
# builds and passes its tests, the i18n check refuses its placeholder texts
# and nothing else, and jec builds with any one module left out.
#
#   scripts/check-modules.sh
set -euo pipefail

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

# Copy the files git knows about, tracked or new, but not ignored ones.
git ls-files -z --cached --others --exclude-standard | tar --null -T - -cf - | tar -xf - -C "$work"
cd "$work"

id=scaffold_check
go run ./tools/new-module -id "$id" -category source
go vet ./...
go test ./internal/modules/... ./cmd/jec/...
go run ./tools/i18nlint ./...

if problems=$(go run ./tools/i18n check 2>&1); then
  echo "the i18n check accepted the placeholder texts" >&2
  exit 1
fi
unexpected=$(grep -v -e "^[a-zA-Z-]*\.json: $id\.module\.[a-z]* is still a placeholder" \
  -e 'problem(s)$' -e '^exit status' <<<"$problems" || true)
if [ -n "$unexpected" ]; then
  printf 'the i18n check found more than the placeholders:\n%s\n' "$unexpected" >&2
  exit 1
fi

# Each module registers itself in internal/modules/all/<id>.go.
for file in internal/modules/all/*.go; do
  module=$(basename "$file" .go)
  case $module in all | *_test) continue ;; esac
  echo "building without $module"
  go build -tags "no_$module" -o /dev/null ./cmd/jec
done
echo "modules: OK"
