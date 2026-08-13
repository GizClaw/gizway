# Milestone 02 — Credit Check is HMAC-only admission, not a reservation.
# covers: internal-gizpay.yaml#checkSubscriptionCredit

POST {{pay_url}}/service/v1/subscription-credit-checks
Authorization: Bearer {{service_token}}
Content-Type: application/json
{"api_key_hmac":"{{active_subscription_hmac}}"}

HTTP 200
[Captures]
credit_before_charge: jsonpath "$.available_microcredits"
[Asserts]
jsonpath "$.status" == "allowed"
jsonpath "$.product_id" exists
jsonpath "$.billing_mode" == "pay_as_you_go"
jsonpath "$.available_microcredits" > 0
jsonpath "$.checked_at" exists
jsonpath "$.recheck_after_seconds" == 301
body not contains "account_id"
body not contains "subscription_id"
body not contains "merchant_id"
body not contains "reservation"
body not contains "lease"


# A second query is an observation of the same Account balance. It does not
# reserve, freeze, lease, or otherwise reduce Credit.
POST {{pay_url}}/service/v1/subscription-credit-checks
Authorization: Bearer {{service_token}}
Content-Type: application/json
{"api_key_hmac":"{{active_subscription_hmac}}"}

HTTP 200
[Asserts]
jsonpath "$.status" == "allowed"
jsonpath "$.available_microcredits" == {{credit_before_charge}}


# Only a posted Charge changes the central balance.
POST {{pay_url}}/service/v1/payg-charges
Authorization: Bearer {{service_token_charger}}
Content-Type: application/json
{
  "external_order_id": "ord_m02_credit_check_deduction",
  "api_key_hmac": "{{active_subscription_hmac}}",
  "gross_microcredits": 1,
  "commissions": [],
  "order": {"type":"contract_probe","title":"credit changes after charge"},
  "service_started_at": "2026-08-13T11:00:00Z",
  "service_completed_at": "2026-08-13T11:00:01Z"
}

HTTP 201
[Asserts]
jsonpath "$.gross_microcredits" == 1


POST {{pay_url}}/service/v1/subscription-credit-checks
Authorization: Bearer {{service_token}}
Content-Type: application/json
{"api_key_hmac":"{{active_subscription_hmac}}"}

HTTP 200
[Asserts]
jsonpath "$.status" == "allowed"
jsonpath "$.available_microcredits" < {{credit_before_charge}}


# Service Account ownership is not a business-resource filter. Another active
# principal with the same machine Role can resolve the same HMAC.
POST {{pay_url}}/service/v1/subscription-credit-checks
Authorization: Bearer {{service_token_other_user}}
Content-Type: application/json
{"api_key_hmac":"{{active_subscription_hmac}}"}

HTTP 200
[Asserts]
jsonpath "$.status" == "allowed"


# A revoked Key remains resolvable for audit/late Charge, but cannot authorize
# a new service start.
POST {{pay_url}}/service/v1/subscription-credit-checks
Authorization: Bearer {{service_token}}
Content-Type: application/json
{"api_key_hmac":"{{revoked_subscription_hmac}}"}

HTTP 200
[Asserts]
jsonpath "$.status" == "denied"
jsonpath "$.available_microcredits" == 0


# Every inactive business state fails closed with the same non-enumerating
# response shape.
POST {{pay_url}}/service/v1/subscription-credit-checks
Authorization: Bearer {{service_token}}
Content-Type: application/json
{"api_key_hmac":"{{zero_balance_subscription_hmac}}"}

HTTP 200
[Asserts]
jsonpath "$.status" == "denied"
jsonpath "$.available_microcredits" == 0


POST {{pay_url}}/service/v1/subscription-credit-checks
Authorization: Bearer {{service_token}}
Content-Type: application/json
{"api_key_hmac":"{{negative_balance_subscription_hmac}}"}

HTTP 200
[Asserts]
jsonpath "$.status" == "denied"
jsonpath "$.available_microcredits" == 0


POST {{pay_url}}/service/v1/subscription-credit-checks
Authorization: Bearer {{service_token}}
Content-Type: application/json
{"api_key_hmac":"{{paused_subscription_hmac}}"}

HTTP 200
[Asserts]
jsonpath "$.status" == "denied"
jsonpath "$.available_microcredits" == 0


POST {{pay_url}}/service/v1/subscription-credit-checks
Authorization: Bearer {{service_token}}
Content-Type: application/json
{"api_key_hmac":"{{inactive_subscription_hmac}}"}

HTTP 200
[Asserts]
jsonpath "$.status" == "denied"
jsonpath "$.available_microcredits" == 0


POST {{pay_url}}/service/v1/subscription-credit-checks
Authorization: Bearer {{service_token}}
Content-Type: application/json
{"api_key_hmac":"{{inactive_product_subscription_hmac}}"}

HTTP 200
[Asserts]
jsonpath "$.status" == "denied"
jsonpath "$.available_microcredits" == 0


# Unknown HMACs use the same non-enumerating denial shape.
POST {{pay_url}}/service/v1/subscription-credit-checks
Authorization: Bearer {{service_token}}
Content-Type: application/json
{"api_key_hmac":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"}

HTTP 200
[Asserts]
jsonpath "$.status" == "denied"
jsonpath "$.available_microcredits" == 0


# Identity fields are derived and therefore forbidden request inputs.
POST {{pay_url}}/service/v1/subscription-credit-checks
Authorization: Bearer {{service_token}}
Content-Type: application/json
{
  "api_key_hmac": "{{active_subscription_hmac}}",
  "account_id": "must-be-rejected"
}

HTTP 400
[Asserts]
jsonpath "$.error.code" == "invalid_request"


# Machine Roles still gate operations even though they do not filter resources.
POST {{pay_url}}/service/v1/subscription-credit-checks
Authorization: Bearer {{service_token_reader_only}}
Content-Type: application/json
{"api_key_hmac":"{{active_subscription_hmac}}"}

HTTP 200
[Asserts]
jsonpath "$.status" == "allowed"
