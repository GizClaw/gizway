#!/bin/sh
set -eu

repository_root="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
cd "${repository_root}"

generator='github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.8.0'
mkdir -p internal/generated/account internal/generated/gizwayadmin internal/generated/gizwaypublic internal/generated/internalgizpay

go run "${generator}" -generate types,client,std-http,spec -package account -o internal/generated/account/api.gen.go api/openapi/account.yaml
go run "${generator}" -generate types,client,std-http,spec -package gizwayadmin -o internal/generated/gizwayadmin/api.gen.go api/openapi/gizway-admin.yaml
go run "${generator}" -generate types,client,std-http,spec -package gizwaypublic -o internal/generated/gizwaypublic/api.gen.go api/openapi/gizway-public.yaml
go run "${generator}" -generate types,client,std-http,spec -package internalgizpay -o internal/generated/internalgizpay/api.gen.go api/openapi/internal-gizpay.yaml
