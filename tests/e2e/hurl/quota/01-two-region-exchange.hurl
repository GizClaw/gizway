# Real-process refactor acceptance: two regional binaries, three isolated
# PostgreSQL databases, certificate-derived node identity, provider execution,
# current-process outbox batching, and central Usage ingestion.

# Quota Exchange accepts the raw API key plus optional Usage and returns only
# the current quota decision. Account and key identifiers are always derived
# inside GizPay and must never appear in either half of the wire contract.
POST {{pay_url}}/internal/v1/quota/exchanges
Content-Type: application/json
{
  "api_key": "giz_e2e_customer_key",
  "usage": []
}
HTTP 200
[Asserts]
body not contains "account_id"
body not contains "api_key_id"
body not contains "key_id"

# Unknown identity fields are rejected instead of being ignored. This keeps a
# Gateway from accidentally turning central Account or API-key IDs into part
# of the cross-region protocol.
POST {{pay_url}}/internal/v1/quota/exchanges
Content-Type: application/json
{
  "api_key": "giz_e2e_customer_key",
  "account_id": "must-not-cross-the-boundary",
  "api_key_id": "must-not-cross-the-boundary",
  "usage": []
}
HTTP 400
[Asserts]
jsonpath "$.error.code" == "invalid_request"

POST {{cn_url}}/admin/v1/auth/login
Idempotency-Key: e2e-cn-login
Content-Type: application/json
{
  "email": "cn-admin@e2e.invalid",
  "password": "e2e-password"
}
HTTP 200
[Captures]
cn_admin: jsonpath "$.access_token"
cn_admin_id: jsonpath "$.administrator.id"

# Each regional Gateway owns its operator identities. The initial CN operator
# creates an automation key in the CN database; that key never exists in
# GizPay or Global and is used for the following local Catalog publication.
POST {{cn_url}}/admin/v1/administrators/{{cn_admin_id}}/api_keys
Authorization: Bearer {{cn_admin}}
Idempotency-Key: e2e-cn-admin-key
Content-Type: application/json
{
  "name": "CN E2E automation"
}
HTTP 201
[Captures]
cn_admin_key: jsonpath "$.secret"
[Asserts]
jsonpath "$.secret" startsWith "gizadm_"

POST {{cn_url}}/admin/v1/rate_publications
Authorization: Bearer {{cn_admin_key}}
Idempotency-Key: e2e-cn-publication
Content-Type: application/json
{
  "source_publication_id": "e2e-cn-publication",
  "effective_at": "2026-08-11T00:00:00Z"
}
HTTP 201
[Asserts]
jsonpath "$.status" == "active"

POST {{global_url}}/admin/v1/auth/login
Idempotency-Key: e2e-global-login
Content-Type: application/json
{
  "email": "global-admin@e2e.invalid",
  "password": "e2e-password"
}
HTTP 200
[Captures]
global_admin: jsonpath "$.access_token"

# A Global administrator session is not an administrator in the CN database.
GET {{cn_url}}/admin/v1/providers
Authorization: Bearer {{global_admin}}
HTTP 401
[Asserts]
jsonpath "$.error.code" == "unauthorized"

POST {{global_url}}/admin/v1/rate_publications
Authorization: Bearer {{global_admin}}
Idempotency-Key: e2e-global-publication
Content-Type: application/json
{
  "source_publication_id": "e2e-global-publication",
  "effective_at": "2026-08-11T00:00:00Z"
}
HTTP 201
[Asserts]
jsonpath "$.status" == "active"

GET {{cn_url}}/readyz
HTTP 200
[Asserts]
jsonpath "$.node_id" == "gw-cn-e2e"
jsonpath "$.region" == "cn"
jsonpath "$.checks.node_identity" == "ready"

GET {{global_url}}/readyz
HTTP 200
[Asserts]
jsonpath "$.node_id" == "gw-global-e2e"
jsonpath "$.region" == "global"
jsonpath "$.checks.node_identity" == "ready"

GET {{cn_url}}/v1/models
Authorization: Bearer giz_e2e_customer_key
HTTP 200
[Asserts]
jsonpath "$.data[0].id" == "cn-model"
body not contains "global-model"

GET {{global_url}}/v1/models
Authorization: Bearer giz_e2e_customer_key
HTTP 200
[Asserts]
jsonpath "$.data[0].id" == "global-model"
body not contains "cn-model"

POST {{cn_url}}/v1/responses
Authorization: Bearer giz_e2e_customer_key
Idempotency-Key: e2e-cn-request-1
Content-Type: application/json
{"model":"cn-model","input":"first CN request"}
HTTP 200

# The 1-second E2E policy has elapsed. This request performs the next Quota
# Exchange and carries the first request's pending UCGID.
POST {{cn_url}}/v1/responses
Authorization: Bearer giz_e2e_customer_key
Idempotency-Key: e2e-cn-request-2
Content-Type: application/json
{"model":"cn-model","input":"second CN request"}
HTTP 200

POST {{global_url}}/v1/responses
Authorization: Bearer giz_e2e_customer_key
Idempotency-Key: e2e-global-request-1
Content-Type: application/json
{"model":"global-model","input":"first Global request"}
HTTP 200

POST {{global_url}}/v1/responses
Authorization: Bearer giz_e2e_customer_key
Idempotency-Key: e2e-global-request-2
Content-Type: application/json
{"model":"global-model","input":"second Global request"}
HTTP 200

POST {{pay_url}}/admin/v1/auth/login
Idempotency-Key: e2e-pay-login
Content-Type: application/json
{
  "email": "admin@e2e.invalid",
  "password": "e2e-password"
}
HTTP 200
[Captures]
pay_admin: jsonpath "$.access_token"

GET {{pay_url}}/admin/v1/received_usage?region=cn&limit=10
Authorization: Bearer {{pay_admin}}
HTTP 200
[Asserts]
jsonpath "$.data[0].region" == "cn"
jsonpath "$.data[0].ucgid" exists
body not contains "giz_e2e_customer_key"

GET {{pay_url}}/admin/v1/received_usage?region=global&limit=10
Authorization: Bearer {{pay_admin}}
HTTP 200
[Asserts]
jsonpath "$.data[0].region" == "global"
jsonpath "$.data[0].ucgid" exists
body not contains "giz_e2e_customer_key"
