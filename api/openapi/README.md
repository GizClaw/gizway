# OpenAPI contracts

The current API has exactly seven root OpenAPI documents:

- `account.yaml`
- `internal-gizpay.yaml`
- `gizpay-webhooks.yaml`
- `gizpay-admin.yaml`
- `gizway-user.yaml`
- `gizway-public.yaml`
- `gizway-admin.yaml`

The two Admin surfaces use only `X-GizWay-Admin-Key`. The value is read from
`admin.initial_key_file`; it is never represented in this contract, returned
by an API, or published in effective runtime configuration. Product and Product
Listing remain GizPay resources. Provider, Model, Model Listing, and Provider
Key remain regional GizWay resources.

Run `./scripts/generate-openapi.sh` after changing a document. The repository
check validates OpenAPI 3.1, route ownership, unique operation IDs, generated
bundles, generated Go code, and Hurl coverage for every operation.

These documents, their generated Go bindings, and the Hurl coverage are the
current executable API contract.
