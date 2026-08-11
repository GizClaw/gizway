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
for bundle in account admin payment; do
    test -s "${bundle_dir}/${bundle}.json"
done
