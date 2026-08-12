# Story 13 — A raw customer API key performs a quota-only Exchange.
#
# Business contract:
# - The key is verified only inside GizPay; the Gateway never sends an Account
#   ID, API Key ID, key projection, lease ID, reservation ID, or QuotaSubject.
# - Empty and omitted Usage are both valid. They only query current spendable
#   balance and create no Usage or financial transaction.
# - Every successful response is a fresh answer about current balance. It is
#   unrelated to an earlier response and must not be accumulated by the caller.
# - The raw key is secret input and must never appear in response or error text.
# covers: internal-gizpay.yaml#exchangeQuota

POST {{pay_url}}/internal/v1/quota/exchanges
Content-Type: application/json
{
  "api_key": "{{gateway_api_key}}",
  "usage": []
}

HTTP 200
[Asserts]
jsonpath "$.status" == "allowed"
jsonpath "$.quota.asset" == "GIZ_CREDIT"
jsonpath "$.quota.microcredits" == 100000000
jsonpath "$.recheck_after_seconds" == 300
jsonpath "$.checked_at" exists
body not contains "giz_story_user_active_1"
body not contains "account_id"
body not contains "api_key_id"
body not contains "lease"
body not contains "reservation"


# Omitting Usage means exactly the same quota-only query and does not depend on
# the result above. In particular, the second answer is not a replenishment.
POST {{pay_url}}/internal/v1/quota/exchanges
Content-Type: application/json
{
  "api_key": "{{gateway_api_key}}"
}

HTTP 200
[Asserts]
jsonpath "$.quota.microcredits" == 100000000


# Schema violations are ordinary bad requests. Identity fields are forbidden,
# because only the raw key may select the account inside GizPay.
POST {{pay_url}}/internal/v1/quota/exchanges
Content-Type: application/json
{
  "api_key": "{{gateway_api_key}}",
  "account_id": "21000000-0000-4000-8000-000000000001"
}

HTTP 400
[Asserts]
jsonpath "$.error.code" == "invalid_request"
body not contains "giz_story_user_active_1"


POST {{pay_url}}/internal/v1/quota/exchanges
Content-Type: application/json
{
  "api_key": "giz_story_invalid"
}

HTTP 401
[Asserts]
jsonpath "$.error.code" == "invalid_api_key"
body not contains "giz_story_invalid"


POST {{pay_url}}/internal/v1/quota/exchanges
Content-Type: application/json
{
  "api_key": "giz_story_user_suspended"
}

HTTP 401
[Asserts]
jsonpath "$.error.code" == "invalid_api_key"
body not contains "giz_story_user_suspended"
