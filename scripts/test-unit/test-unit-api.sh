#!/bin/sh
set -eu
root="$(CDPATH='' cd -- "$(dirname -- "$0")/../.." && pwd)"
for command in hurlfmt go; do
  command -v "$command" >/dev/null 2>&1 || { echo "$command is required to run API contracts" >&2; exit 1; }
done
find "$root/tests/api" -type f -name '*.hurl' -print0 | xargs -0 hurlfmt --check
"$root/scripts/test-unit/test-unit-api-openapi.sh"
"$root/scripts/test-unit/test-unit-api-contracts.sh"
exec "$root/scripts/test-unit/check-e2e-api-seed.sh"
