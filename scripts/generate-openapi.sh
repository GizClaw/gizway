#!/bin/sh
set -eu

repository_root="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
cd "${repository_root}"

generator='github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.8.0'
bundle_dir="$(mktemp -d "${TMPDIR:-/tmp}/gizway-openapi-generate.XXXXXX")"
trap 'rm -rf "${bundle_dir}"' EXIT INT TERM
go run ./cmd/openapi-check -out "${bundle_dir}" >/dev/null
mkdir -p internal/generated/account internal/generated/gizpaywebhooks internal/generated/gizwayuser internal/generated/gizwaypublic internal/generated/internalgizpay

go run "${generator}" -generate types,client,std-http,spec -package account -o internal/generated/account/api.gen.go "${bundle_dir}/account.json"
go run "${generator}" -generate types,client,std-http,spec -package gizpaywebhooks -o internal/generated/gizpaywebhooks/api.gen.go "${bundle_dir}/gizpay-webhooks.json"
go run "${generator}" -generate types,client,std-http,spec -package gizwayuser -o internal/generated/gizwayuser/api.gen.go "${bundle_dir}/gizway-user.json"
go run "${generator}" -generate types,client,std-http,spec -package gizwaypublic -o internal/generated/gizwaypublic/api.gen.go "${bundle_dir}/gizway-public.json"
go run "${generator}" -generate types,client,std-http,spec -package internalgizpay -o internal/generated/internalgizpay/api.gen.go "${bundle_dir}/internal-gizpay.json"

# oapi-codegen emits capitalized wire-validation errors for required headers.
# Keep generated output reproducible while exempting only that style rule.
for generated_file in internal/generated/account/api.gen.go \
    internal/generated/gizpaywebhooks/api.gen.go \
    internal/generated/gizwayuser/api.gen.go \
    internal/generated/gizwaypublic/api.gen.go \
    internal/generated/internalgizpay/api.gen.go; do
    perl -0pi -e 's|(// Code generated .* DO NOT EDIT\.\n)|$1//lint:file-ignore ST1005 generated OpenAPI validation text\n|' "${generated_file}"
done
gofmt -w internal/generated/account/api.gen.go \
    internal/generated/gizpaywebhooks/api.gen.go \
    internal/generated/gizwayuser/api.gen.go \
    internal/generated/gizwaypublic/api.gen.go \
    internal/generated/internalgizpay/api.gen.go
