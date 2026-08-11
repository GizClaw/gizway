# Story 02 — Account restrictions govern discovery and execution together.
#
# Durable policy described by this executable file:
# - Global publication is only the first visibility layer.
# - An Admin may attach an audited allow/deny rule to one account and canonical
#   model. A deny wins; the existence of any allow starts allow-list mode.
# - OpenAI/Anthropic/Gemini discovery, the rich Account catalog, Realtime, and
#   every invocation resolve through the same account policy. A model hidden by
#   policy cannot still be reached by guessing its public ID.
# - Another account is unaffected by the rule.
#
# Fixture: two active personal accounts and globally executable `story-text`.
# Isolation: fresh PostgreSQL for this Hurl file.
# covers: setAccountModelEntitlement
# protocol-case: catalog.account-entitlement discovery-and-execution-same-policy

# Both accounts begin with the globally published model.
GET {{base_url}}/v1/models
Authorization: Bearer {{gateway_api_key}}

HTTP 200
[Asserts]
jsonpath "$.data[0].id" == "story-text"


GET {{base_url}}/v1/models
Authorization: Bearer {{gateway_api_key_two}}

HTTP 200
[Asserts]
jsonpath "$.data[0].id" == "story-text"


# Deny only account two. The immutable model and its prices are not edited.
PUT {{base_url}}/admin/v1/accounts/21000000-0000-4000-8000-000000000002/model_entitlements/81000000-0000-4000-8000-000000000001
Authorization: Bearer {{admin_api_key}}
Idempotency-Key: story-02-deny-account-two-model
Content-Type: application/json
{
  "effect": "deny",
  "reason": "Story contract limits this account before launch"
}

HTTP 200
[Asserts]
jsonpath "$.account_id" == "21000000-0000-4000-8000-000000000002"
jsonpath "$.model_id" == "81000000-0000-4000-8000-000000000001"
jsonpath "$.effect" == "deny"


# Account one remains entitled.
GET {{base_url}}/v1/models
Authorization: Bearer {{gateway_api_key}}

HTTP 200
[Asserts]
jsonpath "$.data" count == 1
jsonpath "$.data[0].id" == "story-text"


# All account-two discovery surfaces now agree that nothing is executable.
GET {{base_url}}/v1/models
Authorization: Bearer {{gateway_api_key_two}}

HTTP 200
[Asserts]
jsonpath "$.data" count == 0


GET {{base_url}}/v1beta/models
Authorization: Bearer {{gateway_api_key_two}}

HTTP 200
[Asserts]
jsonpath "$.models" count == 0


GET {{base_url}}/account/v1/accounts/21000000-0000-4000-8000-000000000002/models
Authorization: Bearer {{active_user_two_key}}

HTTP 200
[Asserts]
jsonpath "$.data" count == 0


# Guessing the canonical public ID cannot bypass discovery policy. The request
# fails before Bifrost and therefore cannot reserve or charge Credit.
POST {{base_url}}/v1/chat/completions
Authorization: Bearer {{gateway_api_key_two}}
Idempotency-Key: story-02-denied-model-invocation
Content-Type: application/json
{
  "model": "story-text",
  "messages": [{"role": "user", "content": "must not reach provider"}]
}

HTTP 404
[Asserts]
jsonpath "$.error.code" == "model_not_found"


GET {{fake_url}}/events

HTTP 200
[Asserts]
jsonpath "$.chat_calls" == 0


# Switching the same policy row to allow re-entitles account two and proves
# that allow-list mode is honored by both discovery and execution.
PUT {{base_url}}/admin/v1/accounts/21000000-0000-4000-8000-000000000002/model_entitlements/81000000-0000-4000-8000-000000000001
Authorization: Bearer {{admin_api_key}}
Idempotency-Key: story-02-allow-account-two-model
Content-Type: application/json
{
  "effect": "allow",
  "reason": "Recorded launch approval for this account"
}

HTTP 200
[Asserts]
jsonpath "$.effect" == "allow"


GET {{base_url}}/v1/models
Authorization: Bearer {{gateway_api_key_two}}

HTTP 200
[Asserts]
jsonpath "$.data" count == 1
jsonpath "$.data[0].id" == "story-text"


POST {{base_url}}/v1/chat/completions
Authorization: Bearer {{gateway_api_key_two}}
Idempotency-Key: story-02-allowed-model-invocation
Content-Type: application/json
{
  "model": "story-text",
  "messages": [{"role": "user", "content": "now authorized"}]
}

HTTP 200
[Asserts]
jsonpath "$.model" == "story-text"


GET {{fake_url}}/events

HTTP 200
[Asserts]
jsonpath "$.chat_calls" == 1
