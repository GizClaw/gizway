#!/bin/sh
set -eu

script_dir="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
repository_root="$(CDPATH= cd -- "${script_dir}/../.." && pwd)"
cd "${repository_root}"

# Parse and recursively resolve every local reference, emit standalone bundles,
# and prove every Gizway-owned method/path exists in the Go router. Generated
# clients, server interfaces and schema types are checked for drift below.
bundle_dir="$(mktemp -d "${TMPDIR:-/tmp}/gizway-openapi.XXXXXX")"
trap 'rm -rf "${bundle_dir}"' EXIT INT TERM

"${GO:-go}" run ./cmd/openapi-check -out "${bundle_dir}"
for bundle in account gizpay-webhooks gizway-user gizway-public internal-gizpay; do
    test -s "${bundle_dir}/${bundle}.json"
done

generated_dir="$(mktemp -d "${TMPDIR:-/tmp}/gizway-openapi-generated.XXXXXX")"
trap 'rm -rf "${bundle_dir}" "${generated_dir}"' EXIT INT TERM
mkdir -p "${generated_dir}/account" "${generated_dir}/gizpaywebhooks" "${generated_dir}/gizwayuser" "${generated_dir}/gizwaypublic" "${generated_dir}/internalgizpay"
generator='github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.8.0'
"${GO:-go}" run "${generator}" -generate types,client,std-http,spec -package account -o "${generated_dir}/account/api.gen.go" "${bundle_dir}/account.json"
"${GO:-go}" run "${generator}" -generate types,client,std-http,spec -package gizpaywebhooks -o "${generated_dir}/gizpaywebhooks/api.gen.go" "${bundle_dir}/gizpay-webhooks.json"
"${GO:-go}" run "${generator}" -generate types,client,std-http,spec -package gizwayuser -o "${generated_dir}/gizwayuser/api.gen.go" "${bundle_dir}/gizway-user.json"
"${GO:-go}" run "${generator}" -generate types,client,std-http,spec -package gizwaypublic -o "${generated_dir}/gizwaypublic/api.gen.go" "${bundle_dir}/gizway-public.json"
"${GO:-go}" run "${generator}" -generate types,client,std-http,spec -package internalgizpay -o "${generated_dir}/internalgizpay/api.gen.go" "${bundle_dir}/internal-gizpay.json"
for package in account gizpaywebhooks gizwayuser gizwaypublic internalgizpay; do
    perl -0pi -e 's|(// Code generated .* DO NOT EDIT\.\n)|$1//lint:file-ignore ST1005 generated OpenAPI validation text\n|' "${generated_dir}/${package}/api.gen.go"
done
gofmt -w "${generated_dir}"/*/api.gen.go
for package in account gizpaywebhooks gizwayuser gizwaypublic internalgizpay; do
    if ! cmp -s "internal/generated/${package}/api.gen.go" "${generated_dir}/${package}/api.gen.go"; then
        echo "internal/generated/${package}/api.gen.go is stale; run ./scripts/generate-openapi.sh" >&2
        exit 1
    fi
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
