# Milestone 02 — Bifrost logging is diagnostic, while financial validation is
# authoritative. Provider success is returned even when Log Store fails; an
# invalid Commission snapshot never becomes a GizPay Charge.

POST {{credit_spy_url}}/test/reset

HTTP 204


# A successful Provider result with actual Gross 7 must consume local Credit
# even when the AI Order transaction fails. Starting from 10 leaves 3, so the
# following admission remains local. If the same call were deducted twice, it
# would force an extra central Credit Check here.
POST {{way_url}}/v1/chat/completions
Authorization: Bearer {{raw_subscription_key_quota_10}}
X-Gizway-Test-Failpoint: ai_order_write
Content-Type: application/json
{"model":"story-text","messages":[{"role":"user","content":"first actual gross seven"}],"stream":false}

HTTP 200
[Asserts]
jsonpath "$.object" == "chat.completion"


GET {{way_url}}/v1/models
Authorization: Bearer {{raw_subscription_key_quota_10}}

HTTP 200


GET {{credit_spy_url}}/test/stats

HTTP 200
[Asserts]
jsonpath "$.credit_check_requests" == 1


# The Outbox failure consumes the next Gross 7: local Credit changes 3 -> -4.
# The next admission must therefore recheck GizPay even though neither failed
# persistence attempt created a central Charge.
POST {{way_url}}/v1/chat/completions
Authorization: Bearer {{raw_subscription_key_quota_10}}
X-Gizway-Test-Failpoint: charge_outbox_write
Content-Type: application/json
{"model":"story-text","messages":[{"role":"user","content":"second actual gross seven"}],"stream":false}

HTTP 200
[Asserts]
jsonpath "$.object" == "chat.completion"


GET {{way_url}}/v1/models
Authorization: Bearer {{raw_subscription_key_quota_10}}

HTTP 200


GET {{credit_spy_url}}/test/stats

HTTP 200
[Asserts]
jsonpath "$.credit_check_requests" == 2


POST {{credit_spy_url}}/test/reset

HTTP 204


# This test-only failpoint fails the Bifrost Log Store write after a successful
# upstream response. It must not replace that response or suppress billing.
POST {{way_url}}/v1/chat/completions
Authorization: Bearer {{raw_subscription_key}}
X-Gizway-Test-Failpoint: bifrost_log_store_write
Content-Type: application/json
{"model":"story-text","messages":[{"role":"user","content":"log store failure is non-fatal"}],"stream":false}

HTTP 200
[Asserts]
jsonpath "$.object" == "chat.completion"


GET {{way_url}}/admin/v1/ai-orders
Authorization: Bearer {{admin_token}}
[Options]
retry: 20
retry-interval: 50ms

HTTP 200
[Asserts]
jsonpath "$.data" count == 1
jsonpath "$.data[0].status" == "charged"
jsonpath "$.data[0].gross_microcredits" == 5
jsonpath "$.data[0].commission_microcredits" == 3


GET {{way_url}}/admin/v1/charge-outbox
Authorization: Bearer {{admin_token}}
[Options]
retry: 20
retry-interval: 50ms

HTTP 200
[Asserts]
jsonpath "$.data" count == 1
jsonpath "$.data[0].status" == "reported"


GET {{credit_spy_url}}/test/stats

HTTP 200
[Asserts]
jsonpath "$.charge_requests" == 1


# Once the Provider response is valid, failures in rating, AI Order persistence,
# or Charge Outbox persistence are internal diagnostics. The successful Provider
# response still wins, and the failed transaction creates no financial work.
POST {{way_url}}/v1/chat/completions
Authorization: Bearer {{raw_subscription_key}}
X-Gizway-Test-Failpoint: rating_failure
Content-Type: application/json
{"model":"story-text","messages":[{"role":"user","content":"rating fails after Provider success"}],"stream":false}

HTTP 200
[Asserts]
jsonpath "$.object" == "chat.completion"


POST {{way_url}}/v1/chat/completions
Authorization: Bearer {{raw_subscription_key}}
X-Gizway-Test-Failpoint: ai_order_write
Content-Type: application/json
{"model":"story-text","messages":[{"role":"user","content":"Order persistence fails after Provider success"}],"stream":false}

HTTP 200
[Asserts]
jsonpath "$.object" == "chat.completion"


POST {{way_url}}/v1/chat/completions
Authorization: Bearer {{raw_subscription_key}}
X-Gizway-Test-Failpoint: charge_outbox_write
Content-Type: application/json
{"model":"story-text","messages":[{"role":"user","content":"Outbox persistence fails after Provider success"}],"stream":false}

HTTP 200
[Asserts]
jsonpath "$.object" == "chat.completion"


POST {{way_url}}/v1/chat/completions
Authorization: Bearer {{raw_subscription_key}}
X-Gizway-Test-Failpoint: selected_key_mapping_failure
Content-Type: application/json
{"model":"story-text","messages":[{"role":"user","content":"selected Key mapping fails after Provider success"}],"stream":false}

HTTP 200
[Asserts]
jsonpath "$.object" == "chat.completion"


GET {{way_url}}/admin/v1/ai-orders
Authorization: Bearer {{admin_token}}

HTTP 200
[Asserts]
jsonpath "$.data" count == 1


GET {{way_url}}/admin/v1/charge-outbox
Authorization: Bearer {{admin_token}}

HTTP 200
[Asserts]
jsonpath "$.data" count == 1


# Anthropic/Gemini conversion happens before rating and persistence. A Provider
# response without public-protocol message content is rejected and never billed.
POST {{way_url}}/v1/messages
Authorization: Bearer {{raw_subscription_key}}
Content-Type: application/json
{"model":"story-text","messages":[{"role":"user","content":"invalid-conversion"}],"max_tokens":16}

HTTP 502
[Asserts]
jsonpath "$.error.code" == "invalid_provider_response"


POST {{way_url}}/v1beta/models/story-text:generateContent
Authorization: Bearer {{raw_subscription_key}}
Content-Type: application/json
{"contents":[{"role":"user","parts":[{"text":"invalid-conversion"}]}]}

HTTP 502
[Asserts]
jsonpath "$.error.code" == "invalid_provider_response"


GET {{credit_spy_url}}/test/stats

HTTP 200
[Asserts]
jsonpath "$.charge_requests" == 1


# Configure a valid nonnegative price set whose actual Provider Commission is
# greater than actual customer Gross for the same normalized usage.
PUT {{way_url}}/admin/v1/models/{{seeded_model_id}}/prices
Authorization: Bearer {{admin_token}}
Content-Type: application/json
{
  "prices": [
    {"metric":"input_token","unit_size":10,"price_microcredits":1},
    {"metric":"output_token","unit_size":10,"price_microcredits":1}
  ]
}

HTTP 200


PUT {{way_url}}/admin/v1/provider-api-keys/{{seeded_bifrost_key_id}}/prices
Authorization: Bearer {{admin_token}}
Content-Type: application/json
{
  "prices": [
    {"model_id":"{{seeded_model_id}}","metric":"input_token","unit_size":10,"commission_microcredits":2},
    {"model_id":"{{seeded_model_id}}","metric":"output_token","unit_size":10,"commission_microcredits":2}
  ]
}

HTTP 200


POST {{way_url}}/v1/chat/completions
Authorization: Bearer {{raw_subscription_key}}
Content-Type: application/json
{"model":"story-text","messages":[{"role":"user","content":"commission exceeds gross"}],"stream":false}

HTTP 200
[Asserts]
jsonpath "$.object" == "chat.completion"


GET {{way_url}}/admin/v1/ai-orders
Authorization: Bearer {{admin_token}}
[Options]
retry: 20
retry-interval: 50ms

HTTP 200
[Asserts]
jsonpath "$.data" count == 2
jsonpath "$.data[?(@.status == 'billing_failed')]" count == 1
jsonpath "$.data[?(@.status == 'billing_failed')].gross_microcredits" contains 2
jsonpath "$.data[?(@.status == 'billing_failed')].commission_microcredits" contains 3
jsonpath "$.data[?(@.status == 'billing_failed')].billing_error.code" contains "commission_exceeds_gross"


# Financially invalid work is diagnosable as an AI Order but never enters the
# Outbox and therefore never reaches the Charge API.
GET {{way_url}}/admin/v1/charge-outbox
Authorization: Bearer {{admin_token}}

HTTP 200
[Asserts]
jsonpath "$.data" count == 1


GET {{credit_spy_url}}/test/stats

HTTP 200
[Asserts]
jsonpath "$.charge_requests" == 1
