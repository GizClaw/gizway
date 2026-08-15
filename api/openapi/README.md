# OpenAPI contracts

Milestone 03 has exactly four root OpenAPI documents:

- `account.yaml`
- `internal-gizpay.yaml`
- `gizway-user.yaml`
- `gizway-public.yaml`

Run `./scripts/generate-openapi.sh` after changing a document. The repository
check validates OpenAPI 3.1, route ownership, unique operation IDs, generated
bundles, generated Go code, and Hurl coverage for every operation.

These documents, their generated Go bindings, and the Hurl coverage are the
current executable API contract.
