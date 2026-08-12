#!/bin/sh
set -eu

script_dir="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
repository_root="$(CDPATH= cd -- "${script_dir}/../.." && pwd)"
cd "${repository_root}"

# Parse and recursively resolve every local reference, emit standalone bundles
# into an ephemeral directory, and prove every Gizway-owned method/path exists
# in the Go router. Nothing generated is committed as a second source of truth.
bundle_dir="$(mktemp -d "${TMPDIR:-/tmp}/gizway-openapi.XXXXXX")"
trap 'rm -rf "${bundle_dir}"' EXIT INT TERM

"${GO:-go}" run ./cmd/openapi-check -out "${bundle_dir}"
for bundle in account gizpay-admin gizway-admin internal-gizpay payment; do
    test -s "${bundle_dir}/${bundle}.json"
done

# The checked-in inventory is a review artifact generated from OpenAPI and
# Hurl ownership, never an independently maintained contract. Fail when it
# drifts so API additions, deletions, or path renames cannot leave stale review
# documentation behind.
"${GO:-go}" run ./cmd/openapi-check -inventory >"${bundle_dir}/API-INVENTORY.md"
if ! cmp -s api/openapi/API-INVENTORY.md "${bundle_dir}/API-INVENTORY.md"; then
    echo "api/openapi/API-INVENTORY.md is stale; regenerate it with: go run ./cmd/openapi-check -inventory" >&2
    exit 1
fi
