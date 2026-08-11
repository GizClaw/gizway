# Story 03 — Reject unaffordable requests before provider execution and release
# reservations after provider failure.
#
# The fake provider exposes only a call counter; database state remains visible
# exclusively through Gizway APIs.
# covers: createCreditTransfer getAccountBalance listGatewayUsage

# Leave user two with 10,000 microcredits, below the request reservation bound.
POST {{base_url}}/account/v1/accounts/21000000-0000-4000-8000-000000000002/transfers
Authorization: Bearer {{active_user_two_key}}
Idempotency-Key: story-03-drain-user-two
Content-Type: application/json
{
  "recipient_account_id": "21000000-0000-4000-8000-000000000001",
  "amount": {"asset":"GIZ_CREDIT","microcredits":49990000},
  "note": "prepare insufficient balance fixture"
}

HTTP 201


POST {{base_url}}/v1/chat/completions
Authorization: Bearer giz_story_user_active_2
Idempotency-Key: story-03-insufficient
Content-Type: application/json
{
  "model":"story-text",
  "messages":[{"role":"user","content":"must not reach provider"}],
  "stream":false
}

HTTP 402
[Asserts]
jsonpath "$.error.code" == "insufficient_balance"


GET {{fake_url}}/events

HTTP 200
[Asserts]
jsonpath "$.chat_calls" == 0


# Provider failure occurs after reservation and must release it without a charge.
POST {{base_url}}/v1/chat/completions
Authorization: Bearer {{gateway_api_key}}
Idempotency-Key: story-03-provider-error
Content-Type: application/json
{
  "model":"story-text",
  "messages":[{"role":"user","content":"provider-error"}],
  "stream":false
}

HTTP 502
[Asserts]
jsonpath "$.error.code" == "provider_error"


GET {{base_url}}/account/v1/accounts/21000000-0000-4000-8000-000000000001/balance
Authorization: Bearer {{active_user_one_key}}

HTTP 200
[Asserts]
jsonpath "$.microcredits" == 149990000


GET {{base_url}}/account/v1/accounts/21000000-0000-4000-8000-000000000001/usage?from=2026-08-10T00%3A00%3A00Z&to=2027-08-11T00%3A00%3A00Z
Authorization: Bearer {{active_user_one_key}}

HTTP 200
[Asserts]
jsonpath "$.data" count == 1
jsonpath "$.data[0].status" == "failed"
jsonpath "$.data[0].charged_microcredits" == 0
