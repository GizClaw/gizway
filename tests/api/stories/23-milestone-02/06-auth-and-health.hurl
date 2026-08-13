# Milestone 02 — identity classes and the single anonymous health surface.

GET {{pay_url}}/healthz

HTTP 200
[Asserts]
jsonpath "$.status" matches "^(healthy|degraded)$"
jsonpath "$.service.kind" == "gizpay"
jsonpath "$.server.name" exists
body not contains "postgres://"
body not contains "access_token"
body not contains "key_hmac"
body not contains "balance_microcredits"


GET {{way_url}}/healthz

HTTP 200
[Asserts]
jsonpath "$.status" matches "^(healthy|degraded)$"
jsonpath "$.service.kind" == "gizway"
jsonpath "$.server.name" exists
body not contains "postgres://"
body not contains "node_id"
body not contains "region"


GET {{pay_url}}/livez

HTTP 404


GET {{way_url}}/readyz

HTTP 404


# Human tokens cannot call machine Charge APIs.
POST {{pay_url}}/service/v1/subscription-credit-checks
Authorization: Bearer {{human_token}}
Content-Type: application/json
{"api_key_hmac":"{{active_subscription_hmac}}"}

HTTP 403
[Asserts]
jsonpath "$.error.code" == "insufficient_role"


# JWT validation checks signature, issuer, audience, expiry, and local
# Principal status independently of Project Roles.
POST {{pay_url}}/service/v1/subscription-credit-checks
Authorization: Bearer {{expired_service_token}}
Content-Type: application/json
{"api_key_hmac":"{{active_subscription_hmac}}"}

HTTP 401
[Asserts]
jsonpath "$.error.code" == "invalid_token"


POST {{pay_url}}/service/v1/subscription-credit-checks
Authorization: Bearer {{wrong_signature_service_token}}
Content-Type: application/json
{"api_key_hmac":"{{active_subscription_hmac}}"}

HTTP 401
[Asserts]
jsonpath "$.error.code" == "invalid_token"


POST {{pay_url}}/service/v1/subscription-credit-checks
Authorization: Bearer {{wrong_audience_service_token}}
Content-Type: application/json
{"api_key_hmac":"{{active_subscription_hmac}}"}

HTTP 401
[Asserts]
jsonpath "$.error.code" == "invalid_token"


POST {{pay_url}}/service/v1/subscription-credit-checks
Authorization: Bearer {{wrong_issuer_service_token}}
Content-Type: application/json
{"api_key_hmac":"{{active_subscription_hmac}}"}

HTTP 401
[Asserts]
jsonpath "$.error.code" == "invalid_token"


POST {{pay_url}}/service/v1/subscription-credit-checks
Authorization: Bearer {{revoked_service_token}}
Content-Type: application/json
{"api_key_hmac":"{{active_subscription_hmac}}"}

HTTP 401
[Asserts]
jsonpath "$.error.code" == "service_principal_revoked"


# Service tokens cannot manage a human Subscription.
GET {{pay_url}}/account/v1/subscriptions
Authorization: Bearer {{service_token}}

HTTP 403
[Asserts]
jsonpath "$.error.code" == "human_identity_required"


# A missing charger Role cannot submit money movement.
POST {{pay_url}}/service/v1/payg-charges
Authorization: Bearer {{service_token_reader_only}}
Content-Type: application/json
{
  "external_order_id": "ord_m02_reader_only",
  "api_key_hmac": "{{active_subscription_hmac}}",
  "gross_microcredits": 1,
  "commissions": [],
  "order": {"type":"ai_call","title":"forbidden"},
  "service_started_at": "2026-08-13T12:00:00Z",
  "service_completed_at": "2026-08-13T12:00:01Z"
}

HTTP 403
[Asserts]
jsonpath "$.error.code" == "insufficient_role"
