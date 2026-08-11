# Story 10 — Security-sensitive rate limits are shared database decisions.
#
# This file is intentionally repetitive: each HTTP attempt is visible because
# Hurl is the durable business specification.
# Production allowances are larger; story mode lowers only the numeric limits
# while exercising the identical PostgreSQL counter and 429 path.
#
# Limits proven in this isolated fixture window:
# - user authentication: 5 attempts per normalized login identity;
# - Gateway invocation: 10 attempts per non-secret API key ID;
# - Credit transfer: 5 valid commands per sender account;
# - payment confirmation: 5 attempts per authenticated payer user.
# Invalid model IDs and terminal payment retries still count as abuse attempts,
# while syntactically invalid transfer bodies are rejected before the counter.
#
# Fixture: story-base, fixed clock; each category has an independent scope.
# protocol-case: security.database-rate-limits auth-model-transfer-payment

# Authentication: do not reveal whether the identity exists. Attempts 1..5
# retain the ordinary 401; attempt 6 is a stable 429 with retry guidance.
POST {{base_url}}/account/v1/auth/login
Idempotency-Key: story-10-login-limit-01
Content-Type: application/json
{
  "email": "active-one@gizway.test",
  "password": "wrong-1"
}
HTTP 401

POST {{base_url}}/account/v1/auth/login
Idempotency-Key: story-10-login-limit-02
Content-Type: application/json
{
  "email": "active-one@gizway.test",
  "password": "wrong-2"
}
HTTP 401

POST {{base_url}}/account/v1/auth/login
Idempotency-Key: story-10-login-limit-03
Content-Type: application/json
{
  "email": "active-one@gizway.test",
  "password": "wrong-3"
}
HTTP 401

POST {{base_url}}/account/v1/auth/login
Idempotency-Key: story-10-login-limit-04
Content-Type: application/json
{
  "email": "active-one@gizway.test",
  "password": "wrong-4"
}
HTTP 401

POST {{base_url}}/account/v1/auth/login
Idempotency-Key: story-10-login-limit-05
Content-Type: application/json
{
  "email": "active-one@gizway.test",
  "password": "wrong-5"
}
HTTP 401

POST {{base_url}}/account/v1/auth/login
Idempotency-Key: story-10-login-limit-06
Content-Type: application/json
{
  "email": "active-one@gizway.test",
  "password": "wrong-6"
}
HTTP 429
[Asserts]
jsonpath "$.error.code" == "rate_limited"


# Gateway attempts 1..10 fail normal catalog resolution. Attempt 11 is stopped
# by the per-key counter before Store candidate resolution or Bifrost work.
POST {{base_url}}/v1/chat/completions
Authorization: Bearer {{gateway_api_key}}
Idempotency-Key: story-10-model-limit-01
Content-Type: application/json
{
  "model": "not-published",
  "messages": [{"role": "user", "content": "1"}]
}
HTTP 404

POST {{base_url}}/v1/chat/completions
Authorization: Bearer {{gateway_api_key}}
Idempotency-Key: story-10-model-limit-02
Content-Type: application/json
{
  "model": "not-published",
  "messages": [{"role": "user", "content": "2"}]
}
HTTP 404

POST {{base_url}}/v1/chat/completions
Authorization: Bearer {{gateway_api_key}}
Idempotency-Key: story-10-model-limit-03
Content-Type: application/json
{
  "model": "not-published",
  "messages": [{"role": "user", "content": "3"}]
}
HTTP 404

POST {{base_url}}/v1/chat/completions
Authorization: Bearer {{gateway_api_key}}
Idempotency-Key: story-10-model-limit-04
Content-Type: application/json
{
  "model": "not-published",
  "messages": [{"role": "user", "content": "4"}]
}
HTTP 404

POST {{base_url}}/v1/chat/completions
Authorization: Bearer {{gateway_api_key}}
Idempotency-Key: story-10-model-limit-05
Content-Type: application/json
{
  "model": "not-published",
  "messages": [{"role": "user", "content": "5"}]
}
HTTP 404

POST {{base_url}}/v1/chat/completions
Authorization: Bearer {{gateway_api_key}}
Idempotency-Key: story-10-model-limit-06
Content-Type: application/json
{
  "model": "not-published",
  "messages": [{"role": "user", "content": "6"}]
}
HTTP 404

POST {{base_url}}/v1/chat/completions
Authorization: Bearer {{gateway_api_key}}
Idempotency-Key: story-10-model-limit-07
Content-Type: application/json
{
  "model": "not-published",
  "messages": [{"role": "user", "content": "7"}]
}
HTTP 404

POST {{base_url}}/v1/chat/completions
Authorization: Bearer {{gateway_api_key}}
Idempotency-Key: story-10-model-limit-08
Content-Type: application/json
{
  "model": "not-published",
  "messages": [{"role": "user", "content": "8"}]
}
HTTP 404

POST {{base_url}}/v1/chat/completions
Authorization: Bearer {{gateway_api_key}}
Idempotency-Key: story-10-model-limit-09
Content-Type: application/json
{
  "model": "not-published",
  "messages": [{"role": "user", "content": "9"}]
}
HTTP 404

POST {{base_url}}/v1/chat/completions
Authorization: Bearer {{gateway_api_key}}
Idempotency-Key: story-10-model-limit-10
Content-Type: application/json
{
  "model": "not-published",
  "messages": [{"role": "user", "content": "10"}]
}
HTTP 404

POST {{base_url}}/v1/chat/completions
Authorization: Bearer {{gateway_api_key}}
Idempotency-Key: story-10-model-limit-11
Content-Type: application/json
{
  "model": "not-published",
  "messages": [{"role": "user", "content": "11"}]
}
HTTP 429
[Asserts]
jsonpath "$.error.code" == "rate_limited"

GET {{fake_url}}/events
HTTP 200
[Asserts]
jsonpath "$.chat_calls" == 0


# Five valid, balanced transfers commit. The sixth valid command is rate
# limited before any balance mutation.
POST {{base_url}}/account/v1/accounts/21000000-0000-4000-8000-000000000001/transfers
Authorization: Bearer {{active_user_one_key}}
Idempotency-Key: story-10-transfer-limit-01
Content-Type: application/json
{
  "recipient_account_id": "21000000-0000-4000-8000-000000000002",
  "amount": {"asset": "GIZ_CREDIT", "microcredits": 1},
  "note": "limit 1"
}
HTTP 201

POST {{base_url}}/account/v1/accounts/21000000-0000-4000-8000-000000000001/transfers
Authorization: Bearer {{active_user_one_key}}
Idempotency-Key: story-10-transfer-limit-02
Content-Type: application/json
{
  "recipient_account_id": "21000000-0000-4000-8000-000000000002",
  "amount": {"asset": "GIZ_CREDIT", "microcredits": 1},
  "note": "limit 2"
}
HTTP 201

POST {{base_url}}/account/v1/accounts/21000000-0000-4000-8000-000000000001/transfers
Authorization: Bearer {{active_user_one_key}}
Idempotency-Key: story-10-transfer-limit-03
Content-Type: application/json
{
  "recipient_account_id": "21000000-0000-4000-8000-000000000002",
  "amount": {"asset": "GIZ_CREDIT", "microcredits": 1},
  "note": "limit 3"
}
HTTP 201

POST {{base_url}}/account/v1/accounts/21000000-0000-4000-8000-000000000001/transfers
Authorization: Bearer {{active_user_one_key}}
Idempotency-Key: story-10-transfer-limit-04
Content-Type: application/json
{
  "recipient_account_id": "21000000-0000-4000-8000-000000000002",
  "amount": {"asset": "GIZ_CREDIT", "microcredits": 1},
  "note": "limit 4"
}
HTTP 201

POST {{base_url}}/account/v1/accounts/21000000-0000-4000-8000-000000000001/transfers
Authorization: Bearer {{active_user_one_key}}
Idempotency-Key: story-10-transfer-limit-05
Content-Type: application/json
{
  "recipient_account_id": "21000000-0000-4000-8000-000000000002",
  "amount": {"asset": "GIZ_CREDIT", "microcredits": 1},
  "note": "limit 5"
}
HTTP 201

POST {{base_url}}/account/v1/accounts/21000000-0000-4000-8000-000000000001/transfers
Authorization: Bearer {{active_user_one_key}}
Idempotency-Key: story-10-transfer-limit-06
Content-Type: application/json
{
  "recipient_account_id": "21000000-0000-4000-8000-000000000002",
  "amount": {"asset": "GIZ_CREDIT", "microcredits": 1},
  "note": "must not post"
}
HTTP 429
[Asserts]
jsonpath "$.error.code" == "rate_limited"

GET {{base_url}}/account/v1/accounts/21000000-0000-4000-8000-000000000001/balance
Authorization: Bearer {{active_user_one_key}}
HTTP 200
[Asserts]
jsonpath "$.microcredits" == 99999995


# Create one legitimate merchant checkout for the payment-confirmation limit.
POST {{base_url}}/account/v1/accounts/22000000-0000-4000-8000-000000000002/api_keys
Authorization: Bearer {{active_user_two_key}}
Idempotency-Key: story-10-rate-payment-key
Content-Type: application/json
{
  "name": "Rate-limit payment key",
  "kind": "payment",
  "scopes": ["pay:intents:write"]
}
HTTP 201
[Captures]
rate_payment_key: jsonpath "$.secret"

POST {{base_url}}/pay/v1/payment_intents
Authorization: Bearer {{rate_payment_key}}
Idempotency-Key: story-10-rate-intent
Content-Type: application/json
{
  "service_id":"23000000-0000-4000-8000-000000000001",
  "external_order_id":"RATE-LIMIT-ORDER",
  "amount":{"asset":"GIZ_CREDIT","microcredits":1000},
  "description":"confirmation limiter",
  "expires_at":"2027-08-10T00:00:00Z",
  "metadata":{}
}
HTTP 201
[Captures]
rate_intent_id: jsonpath "$.id"

# First confirmation settles. Four different retry keys receive the ordinary
# terminal-state conflict and consume attempts 2..5. Attempt 6 is 429.
POST {{base_url}}/pay/v1/payment_intents/{{rate_intent_id}}/confirm
Authorization: Bearer {{active_user_one_key}}
Idempotency-Key: story-10-confirm-limit-01
HTTP 200

POST {{base_url}}/pay/v1/payment_intents/{{rate_intent_id}}/confirm
Authorization: Bearer {{active_user_one_key}}
Idempotency-Key: story-10-confirm-limit-02
HTTP 409

POST {{base_url}}/pay/v1/payment_intents/{{rate_intent_id}}/confirm
Authorization: Bearer {{active_user_one_key}}
Idempotency-Key: story-10-confirm-limit-03
HTTP 409

POST {{base_url}}/pay/v1/payment_intents/{{rate_intent_id}}/confirm
Authorization: Bearer {{active_user_one_key}}
Idempotency-Key: story-10-confirm-limit-04
HTTP 409

POST {{base_url}}/pay/v1/payment_intents/{{rate_intent_id}}/confirm
Authorization: Bearer {{active_user_one_key}}
Idempotency-Key: story-10-confirm-limit-05
HTTP 409

POST {{base_url}}/pay/v1/payment_intents/{{rate_intent_id}}/confirm
Authorization: Bearer {{active_user_one_key}}
Idempotency-Key: story-10-confirm-limit-06
HTTP 429
[Asserts]
jsonpath "$.error.code" == "rate_limited"
