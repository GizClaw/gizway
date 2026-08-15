# M03 machine API identity, ownership snapshot, Charge, and recovery.
# covers: account.yaml#listServiceAccounts account.yaml#createServiceAccount account.yaml#revokeServiceAccount
# covers: internal-gizpay.yaml#checkSubscriptionCredit
# covers: internal-gizpay.yaml#createPAYGCharge internal-gizpay.yaml#getPAYGCharge

GET {{pay_url}}/account/v1/service-accounts
Authorization: Bearer {{human_token}}

HTTP 200


POST {{pay_url}}/account/v1/service-accounts
Authorization: Bearer {{human_token}}
Content-Type: application/json
{"name":"M03 Gateway","roles":["credit_check","charge"]}

HTTP 201
[Captures]
service_account_id: jsonpath "$.id"
[Asserts]
jsonpath "$.credential" exists


DELETE {{pay_url}}/account/v1/service-accounts/{{service_account_id}}
Authorization: Bearer {{human_token}}

HTTP 204


POST {{pay_url}}/service/v1/subscription-credit-checks
Authorization: Bearer {{service_token}}
Content-Type: application/json
{"subscription_key_hmac":"{{active_subscription_hmac}}"}

HTTP 200
[Captures]
account_id: jsonpath "$.account_id"
subscription_id: jsonpath "$.subscription_id"
product_id: jsonpath "$.product_id"
[Asserts]
jsonpath "$.status" == "allowed"
jsonpath "$.billing_mode" == "pay_as_you_go"
jsonpath "$.owner_identity_issuer" exists
jsonpath "$.owner_identity_subject" exists
jsonpath "$.checked_at" exists
jsonpath "$.recheck_after_seconds" == 300
body not contains "recheck_at"


POST {{pay_url}}/service/v1/payg-charges
Authorization: Bearer {{service_token_charger}}
Content-Type: application/json
{
  "external_order_id": "m03-order-001",
  "subscription_key_hmac": "{{active_subscription_hmac}}",
  "gross_microcredits": 10,
  "commissions": [],
  "order": {"model":"{{seeded_model_id}}"},
  "service_started_at": "2026-08-14T12:00:00Z",
  "service_completed_at": "2026-08-14T12:00:01Z"
}

HTTP 201
[Captures]
external_order_id: jsonpath "$.external_order_id"
[Asserts]
jsonpath "$.gross_microcredits" == 10


GET {{pay_url}}/service/v1/payg-charges/{{external_order_id}}
Authorization: Bearer {{service_token_charger}}

HTTP 200
[Asserts]
jsonpath "$.external_order_id" == "{{external_order_id}}"


POST {{pay_url}}/service/v1/payg-charges
Authorization: Bearer {{service_token_charger}}
Content-Type: application/json
{
  "external_order_id": "{{external_order_id}}",
  "subscription_key_hmac": "{{active_subscription_hmac}}",
  "gross_microcredits": 10,
  "commissions": [],
  "order": {"model":"{{seeded_model_id}}"},
  "service_started_at": "2026-08-14T12:00:00Z",
  "service_completed_at": "2026-08-14T12:00:01Z"
}

HTTP 409


# A completed call may overdraw the Account. Admission is denied only afterwards.
POST {{pay_url}}/service/v1/payg-charges
Authorization: Bearer {{service_token_charger}}
Content-Type: application/json
{
  "external_order_id": "m03-negative-balance-order",
  "subscription_key_hmac": "{{negative_balance_hmac}}",
  "gross_microcredits": 10,
  "commissions": [],
  "order": {"model":"{{seeded_model_id}}"},
  "service_started_at": "2026-08-14T12:00:00Z",
  "service_completed_at": "2026-08-14T12:00:01Z"
}

HTTP 201


GET {{pay_url}}/account/v1/accounts/{{account_id_two}}/balance
Authorization: Bearer {{human_token_two}}

HTTP 200
[Asserts]
jsonpath "$.balance_microcredits" == -5


POST {{pay_url}}/service/v1/subscription-credit-checks
Authorization: Bearer {{service_token}}
Content-Type: application/json
{"subscription_key_hmac":"{{negative_balance_hmac}}"}

HTTP 200
[Asserts]
jsonpath "$.status" == "denied"
jsonpath "$.available_microcredits" == 0


# Revocation does not erase an invocation that started at the revocation boundary.
POST {{pay_url}}/service/v1/payg-charges
Authorization: Bearer {{service_token_charger}}
Content-Type: application/json
{
  "external_order_id": "m03-revoked-key-started-before",
  "subscription_key_hmac": "{{revoked_subscription_hmac}}",
  "gross_microcredits": 10,
  "commissions": [],
  "order": {"model":"{{seeded_model_id}}"},
  "service_started_at": "{{revoked_at}}",
  "service_completed_at": "{{revoked_at}}"
}

HTTP 201


# A call that starts after revocation is not chargeable through that Key.
POST {{pay_url}}/service/v1/payg-charges
Authorization: Bearer {{service_token_charger}}
Content-Type: application/json
{
  "external_order_id": "m03-revoked-key-started-after",
  "subscription_key_hmac": "{{revoked_subscription_hmac}}",
  "gross_microcredits": 10,
  "commissions": [],
  "order": {"model":"{{seeded_model_id}}"},
  "service_started_at": "{{after_revocation_time}}",
  "service_completed_at": "{{after_revocation_time}}"
}

HTTP 404
[Asserts]
jsonpath "$.error.code" == "subscription_key_not_found"
