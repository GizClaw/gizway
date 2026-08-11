# Story 10 — Credential classes and tenant isolation are checked before data.
#
# Product behavior:
# - User sessions, Gateway keys, Payment keys and Admin credentials are distinct
#   authentication classes even though each is carried in Bearer syntax.
# - A valid credential on the wrong surface receives 401/403 and cannot infer
#   whether a target resource exists.
# - Account reads always join through the authenticated owner. Guessing another
#   account UUID cannot expose its balance, keys or usage.
# - JSON decoders reject unknown fields so clients cannot smuggle ignored
#   security-sensitive options into commands.
#
# Fixture: two active users, an approved merchant, Gateway/Admin credentials.
# Isolation: fresh PostgreSQL per file.
# covers: getCurrentUser getAccountBalance listApiKeys listGatewayUsage

POST {{base_url}}/account/v1/accounts/22000000-0000-4000-8000-000000000002/api_keys
Authorization: Bearer {{active_user_two_key}}
Idempotency-Key: story-10-payment-key
Content-Type: application/json
{
  "name": "Security matrix payment key",
  "kind": "payment",
  "scopes": ["pay:intents:write", "pay:transactions:read"]
}

HTTP 201
[Captures]
payment_key: jsonpath "$.secret"


# Gateway read scopes are real least-privilege capabilities, not decorative
# strings. `account:self` may discover models and read the bound account's
# balance/catalog, while it cannot invoke a model or read usage.
POST {{base_url}}/account/v1/accounts/21000000-0000-4000-8000-000000000001/api_keys
Authorization: Bearer {{active_user_one_key}}
Idempotency-Key: story-10-account-self-key
Content-Type: application/json
{
  "name": "Account self read only",
  "kind": "gateway",
  "scopes": ["account:self"]
}

HTTP 201
[Captures]
account_self_key: jsonpath "$.secret"


# `gateway:usage:read` is intentionally narrower still: it can read only usage
# for the exact account to which this non-secret key ID is bound.
POST {{base_url}}/account/v1/accounts/21000000-0000-4000-8000-000000000001/api_keys
Authorization: Bearer {{active_user_one_key}}
Idempotency-Key: story-10-usage-read-key
Content-Type: application/json
{
  "name": "Usage read only",
  "kind": "gateway",
  "scopes": ["gateway:usage:read"]
}

HTTP 201
[Captures]
usage_read_key: jsonpath "$.secret"


GET {{base_url}}/account/v1/accounts/21000000-0000-4000-8000-000000000001/balance
Authorization: Bearer {{account_self_key}}

HTTP 200


GET {{base_url}}/account/v1/accounts/21000000-0000-4000-8000-000000000001/models
Authorization: Bearer {{account_self_key}}

HTTP 200


GET {{base_url}}/v1/models
Authorization: Bearer {{account_self_key}}

HTTP 200


GET {{base_url}}/account/v1/accounts/21000000-0000-4000-8000-000000000001/usage?from=2026-08-10T00%3A00%3A00Z&to=2027-08-11T00%3A00%3A00Z
Authorization: Bearer {{account_self_key}}

HTTP 403


POST {{base_url}}/v1/chat/completions
Authorization: Bearer {{account_self_key}}
Idempotency-Key: story-10-read-key-cannot-invoke
Content-Type: application/json
{
  "model": "story-text",
  "messages": [{"role": "user", "content": "must not reach provider"}]
}

HTTP 403


GET {{base_url}}/account/v1/accounts/21000000-0000-4000-8000-000000000001/usage?from=2026-08-10T00%3A00%3A00Z&to=2027-08-11T00%3A00%3A00Z
Authorization: Bearer {{usage_read_key}}

HTTP 200


GET {{base_url}}/account/v1/accounts/21000000-0000-4000-8000-000000000001/balance
Authorization: Bearer {{usage_read_key}}

HTTP 403


GET {{base_url}}/v1/models
Authorization: Bearer {{usage_read_key}}

HTTP 403


# Ownership of the user is not enough: a Gateway key never expands from its
# bound account to another account, including another account of that owner.
GET {{base_url}}/account/v1/accounts/21000000-0000-4000-8000-000000000002/balance
Authorization: Bearer {{account_self_key}}

HTTP 403


# User session cannot invoke the provider-compatible Gateway.
GET {{base_url}}/v1/models
Authorization: Bearer {{active_user_one_key}}

HTTP 401


# Query-string credentials are a Gemini compatibility exception only. OpenAI
# and Anthropic routes reject them so reusable keys never leak through ordinary
# URL/access logging; the same key remains valid on the documented Gemini path.
GET {{base_url}}/v1/models?key={{gateway_api_key}}

HTTP 401


GET {{base_url}}/v1beta/models?key={{gateway_api_key}}

HTTP 200


# Gateway key cannot become an Account browser session.
GET {{base_url}}/account/v1/me
Authorization: Bearer {{gateway_api_key}}

HTTP 401


# Payment key is not a Gateway key.
GET {{base_url}}/v1/models
Authorization: Bearer {{payment_key}}

HTTP 401


# Admin key is not a customer Account session or Payment key.
GET {{base_url}}/account/v1/me
Authorization: Bearer {{admin_api_key}}

HTTP 401


GET {{base_url}}/pay/v1/transactions
Authorization: Bearer {{admin_api_key}}

HTTP 401


# Customer credentials cannot enter Admin operations.
GET {{base_url}}/admin/v1/overview
Authorization: Bearer {{payment_key}}

HTTP 401


GET {{base_url}}/admin/v1/overview
Authorization: Bearer {{gateway_api_key}}

HTTP 401


# User one cannot enumerate user two's account resources.
GET {{base_url}}/account/v1/accounts/21000000-0000-4000-8000-000000000002/balance
Authorization: Bearer {{active_user_one_key}}

HTTP 404


GET {{base_url}}/account/v1/accounts/21000000-0000-4000-8000-000000000002/api_keys
Authorization: Bearer {{active_user_one_key}}

HTTP 404


GET {{base_url}}/account/v1/accounts/21000000-0000-4000-8000-000000000002/usage?from=2026-08-10T00%3A00%3A00Z&to=2027-08-11T00%3A00%3A00Z
Authorization: Bearer {{active_user_one_key}}

HTTP 404


# Strict decoding rejects an unknown field rather than silently ignoring it.
POST {{base_url}}/pay/v1/payment_intents
Authorization: Bearer {{payment_key}}
Idempotency-Key: story-10-unknown-field
Content-Type: application/json
{
  "external_order_id": "SECURITY-UNKNOWN",
  "amount": {"asset": "GIZ_CREDIT", "microcredits": 1000},
  "expires_at": "2027-08-10T00:00:00Z",
  "merchant_supplied_fee": 0
}

HTTP 400
[Asserts]
jsonpath "$.error.code" == "invalid_request"
