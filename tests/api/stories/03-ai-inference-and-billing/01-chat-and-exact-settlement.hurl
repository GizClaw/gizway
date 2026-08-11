# Story 03 — Invoke a canonical model and settle exact measured usage once.
#
# Product behavior:
# - A Gateway key authorizes before provider execution.
# - Gizway resolves an enabled, currently priced database variant.
# - Bifrost calls the runner-owned Fake AI Provider and normalizes usage.
# - Integer ceiling pricing is exact: input ceil(10*1800/1000)=18;
#   output ceil(5*3600/1000)=18; total=36 microcredits.
# - Usage, price snapshots, balanced ledger entries, response replay, and final
#   request state commit atomically.
#
# Fixture: story-base and a separate Fake AI Provider process.
# Isolation: fresh PostgreSQL, Gizway, Bifrost, and fake provider per file.
# protocol-case: openai.chat.nonstream exact-settlement replay conflict
# protocol covers: OpenAI POST /v1/chat/completions
# covers: getAccountBalance listGatewayUsage listAccountTransactions

# An explicitly present zero is invalid. It is not an omitted optional limit,
# must not reserve the catalog default, and must be rejected before Bifrost is
# called. This applies equally to streaming and non-streaming requests because
# validation happens before response headers are committed.
POST {{base_url}}/v1/chat/completions
Authorization: Bearer {{gateway_api_key}}
Idempotency-Key: story-03-chat-zero-output
Content-Type: application/json
{
  "model": "story-text",
  "messages": [{"role": "user", "content": "invalid zero output"}],
  "max_tokens": 0,
  "stream": false
}

HTTP 400
[Asserts]
jsonpath "$.error.code" == "invalid_request"

POST {{base_url}}/v1/chat/completions
Authorization: Bearer {{gateway_api_key}}
Idempotency-Key: story-03-chat-one
Content-Type: application/json
{
  "model": "story-text",
  "messages": [{"role": "user", "content": "hello from Hurl"}],
  "stream": false
}

HTTP 200
[Asserts]
jsonpath "$.object" == "chat.completion"
jsonpath "$.model" == "story-text"
jsonpath "$.choices[0].message.content" == "deterministic story response"
jsonpath "$.usage.prompt_tokens" == 10
jsonpath "$.usage.completion_tokens" == 5


# An exact retry returns the persisted response and creates no second charge.
POST {{base_url}}/v1/chat/completions
Authorization: Bearer {{gateway_api_key}}
Idempotency-Key: story-03-chat-one
Content-Type: application/json
{
  "model": "story-text",
  "messages": [{"role": "user", "content": "hello from Hurl"}],
  "stream": false
}

HTTP 200
[Asserts]
jsonpath "$.id" == "fake-chat-001"


# A changed payload under the same key is rejected before another provider call.
POST {{base_url}}/v1/chat/completions
Authorization: Bearer {{gateway_api_key}}
Idempotency-Key: story-03-chat-one
Content-Type: application/json
{
  "model": "story-text",
  "messages": [{"role": "user", "content": "changed payload"}],
  "stream": false
}

HTTP 409
[Asserts]
jsonpath "$.error.code" == "idempotency_conflict"


GET {{fake_url}}/events

HTTP 200
[Asserts]
jsonpath "$.chat_calls" == 1


GET {{base_url}}/account/v1/accounts/21000000-0000-4000-8000-000000000001/balance
Authorization: Bearer {{active_user_one_key}}

HTTP 200
[Asserts]
jsonpath "$.microcredits" == 99999964


GET {{base_url}}/account/v1/accounts/21000000-0000-4000-8000-000000000001/usage?from=2026-08-10T00%3A00%3A00Z&to=2027-08-11T00%3A00%3A00Z
Authorization: Bearer {{active_user_one_key}}

HTTP 200
[Asserts]
jsonpath "$.data" count == 1
jsonpath "$.data[0].input_tokens" == 10
jsonpath "$.data[0].output_tokens" == 5
jsonpath "$.data[0].charged_microcredits" == 36


GET {{base_url}}/account/v1/accounts/21000000-0000-4000-8000-000000000001/transactions
Authorization: Bearer {{active_user_one_key}}

HTTP 200
[Asserts]
jsonpath "$.data" count == 2
jsonpath "$.data[0].type" == "gateway_usage"
jsonpath "$.data[0].amount.microcredits" == 36


# Cached prompt tokens are a priced subset of prompt_tokens, not an extra
# quantity and not ordinary input. Exact charge: 6 normal input -> 11, 4 cached
# input -> 4, 5 output -> 18; total 33 microcredits.
POST {{base_url}}/v1/chat/completions
Authorization: Bearer {{gateway_api_key}}
Idempotency-Key: story-03-chat-cached-input
Content-Type: application/json
{
  "model": "story-text",
  "messages": [{"role": "user", "content": "cached chat"}],
  "stream": false
}

HTTP 200
[Asserts]
jsonpath "$.usage.prompt_tokens" == 10
jsonpath "$.usage.prompt_tokens_details.cached_tokens" == 4


GET {{base_url}}/account/v1/accounts/21000000-0000-4000-8000-000000000001/usage?from=2026-08-10T00%3A00%3A00Z&to=2027-08-11T00%3A00%3A00Z
Authorization: Bearer {{active_user_one_key}}

HTTP 200
[Asserts]
jsonpath "$.data" count == 2
jsonpath "$.data[0].input_tokens" == 6
jsonpath "$.data[0].cached_input_tokens" == 4
jsonpath "$.data[0].output_tokens" == 5
jsonpath "$.data[0].charged_microcredits" == 33
jsonpath "$.data[0].charges" count == 3
jsonpath "$.data[0].charges[0].metric" == "cached_input_token"
jsonpath "$.data[0].charges[0].quantity" == 4
jsonpath "$.data[0].charges[0].base_price_microcredits" == 1000
jsonpath "$.data[0].charges[0].effective_price_microcredits" == 900
jsonpath "$.data[0].charges[0].discount_bps" == 1000
jsonpath "$.data[0].charges[0].charged_microcredits" == 4
jsonpath "$.data[0].charges[1].metric" == "input_token"
jsonpath "$.data[0].charges[1].quantity" == 6
jsonpath "$.data[0].charges[1].charged_microcredits" == 11
jsonpath "$.data[0].charges[2].metric" == "output_token"
jsonpath "$.data[0].charges[2].quantity" == 5
jsonpath "$.data[0].charges[2].charged_microcredits" == 18


# Pagination and API-key attribution are part of the usage contract, not
# decorative OpenAPI parameters. With two completed calls, limit=1 must expose
# a continuation cursor; the second page must contain exactly the other row.
GET {{base_url}}/account/v1/accounts/21000000-0000-4000-8000-000000000001/usage?from=2026-08-10T00%3A00%3A00Z&to=2027-08-11T00%3A00%3A00Z&api_key_id=31000000-0000-4000-8000-000000000001&limit=1
Authorization: Bearer {{active_user_one_key}}

HTTP 200
[Captures]
usage_cursor: jsonpath "$.page.next_cursor"
[Asserts]
jsonpath "$.data" count == 1
jsonpath "$.data[0].api_key_id" == "31000000-0000-4000-8000-000000000001"
jsonpath "$.page.has_more" == true


GET {{base_url}}/account/v1/accounts/21000000-0000-4000-8000-000000000001/usage?from=2026-08-10T00%3A00%3A00Z&to=2027-08-11T00%3A00%3A00Z&api_key_id=31000000-0000-4000-8000-000000000001&limit=1&cursor={{usage_cursor}}
Authorization: Bearer {{active_user_one_key}}

HTTP 200
[Asserts]
jsonpath "$.data" count == 1
jsonpath "$.page.has_more" == false
jsonpath "$.page.next_cursor" == null


# A key belonging to another account cannot widen the result; it is a filter
# inside the already-owned account, never a replacement tenant selector.
GET {{base_url}}/account/v1/accounts/21000000-0000-4000-8000-000000000001/usage?from=2026-08-10T00%3A00%3A00Z&to=2027-08-11T00%3A00%3A00Z&api_key_id=31000000-0000-4000-8000-000000000002&limit=1
Authorization: Bearer {{active_user_one_key}}

HTTP 200
[Asserts]
jsonpath "$.data" count == 0


# Malformed pagination is rejected rather than silently falling back to an
# unbounded query.
GET {{base_url}}/account/v1/accounts/21000000-0000-4000-8000-000000000001/usage?from=2026-08-10T00%3A00%3A00Z&to=2027-08-11T00%3A00%3A00Z&limit=101
Authorization: Bearer {{active_user_one_key}}

HTTP 400
[Asserts]
jsonpath "$.error.code" == "invalid_request"


GET {{base_url}}/account/v1/accounts/21000000-0000-4000-8000-000000000001/balance
Authorization: Bearer {{active_user_one_key}}

HTTP 200
[Asserts]
jsonpath "$.microcredits" == 99999931


GET {{fake_url}}/events

HTTP 200
[Asserts]
jsonpath "$.chat_calls" == 2
