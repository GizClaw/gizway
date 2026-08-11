# Story 08 — A fully discounted model remains executable.
#
# Why this contract exists:
# - Catalog prices intentionally allow zero effective customer price. This is
#   useful for trials, grants, and a 100% discount; zero is not “missing”.
# - Discovery and execution must agree. If the model is listed as usable, the
#   invocation cannot later fail merely because its reservation is zero.
# - A free request still persists measured usage and immutable per-metric price
#   snapshots. It creates neither a positive Credit reservation nor an empty
#   ledger posting, and replay remains exactly-once.
#
# Fixture: story-base; a fresh PostgreSQL database is created for this file.
# protocol-case: openai.chat.zero-price executable-usage-no-debit

POST {{base_url}}/admin/v1/models
Authorization: Bearer {{admin_api_key}}
Idempotency-Key: story-08-free-model
Content-Type: application/json
{
  "slug": "story-free-text",
  "name": "Story Fully Discounted Text",
  "modality": ["text"],
  "metadata": {"test_contract": "zero-price-is-executable"}
}

HTTP 201
[Captures]
free_model_id: jsonpath "$.id"


POST {{base_url}}/admin/v1/models/{{free_model_id}}/variants
Authorization: Bearer {{admin_api_key}}
Idempotency-Key: story-08-free-variant
Content-Type: application/json
{
  "provider_endpoint_id": "71000000-0000-4000-8000-000000000001",
  "provider_model_name": "fake-free-text-v1",
  "variant_slug": "story-free-provider",
  "capabilities": {"chat": true},
  "context_window": 8192,
  "max_output_tokens": 2048
}

HTTP 201
[Captures]
free_variant_id: jsonpath "$.id"


# All required Chat metrics exist. Baseline price records the normal price,
# while effective price zero and discount_bps 10000 make the model truly free.
POST {{base_url}}/admin/v1/model_variants/{{free_variant_id}}/prices
Authorization: Bearer {{admin_api_key}}
Idempotency-Key: story-08-free-input-price
Content-Type: application/json
{
  "metric": "input_token",
  "unit_size": 1000,
  "upstream_cost_microcredits": 1000,
  "base_customer_price_microcredits": 2000,
  "customer_price_microcredits": 0,
  "discount_bps": 10000,
  "valid_from": "2026-08-11T00:00:00Z",
  "valid_until": null
}

HTTP 201


POST {{base_url}}/admin/v1/model_variants/{{free_variant_id}}/prices
Authorization: Bearer {{admin_api_key}}
Idempotency-Key: story-08-free-cached-price
Content-Type: application/json
{
  "metric": "cached_input_token",
  "unit_size": 1000,
  "upstream_cost_microcredits": 500,
  "base_customer_price_microcredits": 1000,
  "customer_price_microcredits": 0,
  "discount_bps": 10000,
  "valid_from": "2026-08-11T00:00:00Z",
  "valid_until": null
}

HTTP 201


POST {{base_url}}/admin/v1/model_variants/{{free_variant_id}}/prices
Authorization: Bearer {{admin_api_key}}
Idempotency-Key: story-08-free-output-price
Content-Type: application/json
{
  "metric": "output_token",
  "unit_size": 1000,
  "upstream_cost_microcredits": 2000,
  "base_customer_price_microcredits": 4000,
  "customer_price_microcredits": 0,
  "discount_bps": 10000,
  "valid_from": "2026-08-11T00:00:00Z",
  "valid_until": null
}

HTTP 201


# Visibility uses required metric presence, not “price greater than zero”.
GET {{base_url}}/v1/models
Authorization: Bearer {{gateway_api_key}}

HTTP 200
[Asserts]
jsonpath "$.data[?(@.id == 'story-free-text')].id" == "story-free-text"


# Protocol discovery is capability-specific. The new variant declares only
# Chat, so Gemini and Anthropic must not advertise generation/messages support
# for it merely because the canonical model is globally executable somewhere.
GET {{base_url}}/v1beta/models
Authorization: Bearer {{gateway_api_key}}

HTTP 200
[Asserts]
jsonpath "$.models" count == 1
jsonpath "$.models[0].name" == "models/story-text"


GET {{base_url}}/v1/models
x-api-key: {{gateway_api_key}}
anthropic-version: 2023-06-01

HTTP 200
[Asserts]
jsonpath "$.data" count == 1
jsonpath "$.data[0].id" == "story-text"


GET {{base_url}}/account/v1/accounts/21000000-0000-4000-8000-000000000001/balance
Authorization: Bearer {{active_user_one_key}}

HTTP 200
[Captures]
balance_before: jsonpath "$.microcredits"


POST {{base_url}}/v1/chat/completions
Authorization: Bearer {{gateway_api_key}}
Idempotency-Key: story-08-free-chat
Content-Type: application/json
{
  "model": "story-free-text",
  "messages": [{"role": "user", "content": "a free but real provider call"}],
  "stream": false
}

HTTP 200
[Asserts]
jsonpath "$.model" == "story-free-text"
jsonpath "$.usage.prompt_tokens" == 10
jsonpath "$.usage.completion_tokens" == 5


# The provider result replays even though there was no positive reservation.
POST {{base_url}}/v1/chat/completions
Authorization: Bearer {{gateway_api_key}}
Idempotency-Key: story-08-free-chat
Content-Type: application/json
{
  "model": "story-free-text",
  "messages": [{"role": "user", "content": "a free but real provider call"}],
  "stream": false
}

HTTP 200
[Asserts]
jsonpath "$.model" == "story-free-text"


GET {{base_url}}/account/v1/accounts/21000000-0000-4000-8000-000000000001/balance
Authorization: Bearer {{active_user_one_key}}

HTTP 200
[Asserts]
jsonpath "$.microcredits" == {{balance_before}}


GET {{base_url}}/account/v1/accounts/21000000-0000-4000-8000-000000000001/usage?from=2026-08-11T00%3A00%3A00Z&to=2026-08-12T00%3A00%3A00Z
Authorization: Bearer {{active_user_one_key}}

HTTP 200
[Asserts]
jsonpath "$.data" count == 1
jsonpath "$.data[0].charged_microcredits" == 0
jsonpath "$.data[0].charges" count == 2
jsonpath "$.data[0].charges[0].effective_price_microcredits" == 0
jsonpath "$.data[0].charges[0].discount_bps" == 10000


GET {{fake_url}}/events

HTTP 200
[Asserts]
jsonpath "$.chat_calls" == 1
