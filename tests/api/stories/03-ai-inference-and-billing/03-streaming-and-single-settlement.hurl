# Story 03 — Stream provider chunks and settle the complete request once.
#
# Product behavior:
# - stream=true uses Bifrost's actual streaming path to the external fake; the
#   API does not manufacture chunks from a buffered non-streaming response.
# - The provider's terminal usage record is authoritative. Earlier deltas are
#   forwarded immediately but never post partial ledger transactions.
# - Exact integer pricing is input ceil(12*1800/1000)=22 plus output
#   ceil(7*3600/1000)=26, for one 48-microcredit settlement.
# - Retrying the same command replays persisted public chunks and never calls
#   the provider or ledger a second time.
#
# Fixture: story-base and the runner-owned OpenAI-compatible fake process.
# Isolation: fresh PostgreSQL, Gizway, Bifrost, and fake provider per Hurl file.
# protocol-case: openai.chat.streaming terminal-usage replay
# protocol covers: OpenAI streaming POST /v1/chat/completions
# covers: getAccountBalance listGatewayUsage

POST {{base_url}}/v1/chat/completions
Authorization: Bearer {{gateway_api_key}}
Idempotency-Key: story-03-stream-one
Content-Type: application/json
{
  "model": "story-text",
  "messages": [{"role": "user", "content": "stream from Hurl"}],
  "stream": true,
  "max_tokens": 64
}

HTTP 200
[Asserts]
header "Content-Type" startsWith "text/event-stream"
body contains "deterministic "
body contains "stream response"
body contains "\"model\":\"story-text\""
body contains "data: [DONE]"


# Replay is an exact persisted stream and does not hit the fake again.
POST {{base_url}}/v1/chat/completions
Authorization: Bearer {{gateway_api_key}}
Idempotency-Key: story-03-stream-one
Content-Type: application/json
{
  "model": "story-text",
  "messages": [{"role": "user", "content": "stream from Hurl"}],
  "stream": true,
  "max_tokens": 64
}

HTTP 200
[Asserts]
body contains "fake-stream-001"
body contains "data: [DONE]"


# A streaming command still performs idempotency validation before committing
# SSE headers. Reusing the key with another payload is a real conflict, not an
# HTTP 200 stream_failed frame; this distinction is also used by crash recovery
# to separate permanent corruption from a benign concurrent lease holder.
POST {{base_url}}/v1/chat/completions
Authorization: Bearer {{gateway_api_key}}
Idempotency-Key: story-03-stream-one
Content-Type: application/json
{
  "model": "story-text",
  "messages": [{"role": "user", "content": "changed streaming payload"}],
  "stream": true,
  "max_tokens": 64
}

HTTP 409
[Asserts]
header "Content-Type" startsWith "application/json"
jsonpath "$.error.code" == "idempotency_conflict"


GET {{fake_url}}/events

HTTP 200
[Asserts]
jsonpath "$.stream_calls" == 1


GET {{base_url}}/account/v1/accounts/21000000-0000-4000-8000-000000000001/balance
Authorization: Bearer {{active_user_one_key}}

HTTP 200
[Asserts]
jsonpath "$.microcredits" == 99999952


GET {{base_url}}/account/v1/accounts/21000000-0000-4000-8000-000000000001/usage?from=2026-08-10T00%3A00%3A00Z&to=2027-08-11T00%3A00%3A00Z
Authorization: Bearer {{active_user_one_key}}

HTTP 200
[Asserts]
jsonpath "$.data" count == 1
jsonpath "$.data[0].input_tokens" == 12
jsonpath "$.data[0].output_tokens" == 7
jsonpath "$.data[0].charged_microcredits" == 48


# Streaming terminal usage carries the same cached-token breakdown. Exact
# second charge: 8 normal input -> 15, 4 cached -> 4, 7 output -> 26; total 45.
POST {{base_url}}/v1/chat/completions
Authorization: Bearer {{gateway_api_key}}
Idempotency-Key: story-03-stream-cached-input
Content-Type: application/json
{
  "model": "story-text",
  "messages": [{"role": "user", "content": "cached stream"}],
  "stream": true,
  "max_tokens": 64
}

HTTP 200
[Asserts]
body contains "\"cached_tokens\":4"
body contains "data: [DONE]"


GET {{base_url}}/account/v1/accounts/21000000-0000-4000-8000-000000000001/usage?from=2026-08-10T00%3A00%3A00Z&to=2027-08-11T00%3A00%3A00Z
Authorization: Bearer {{active_user_one_key}}

HTTP 200
[Asserts]
jsonpath "$.data" count == 2
jsonpath "$.data[0].input_tokens" == 8
jsonpath "$.data[0].cached_input_tokens" == 4
jsonpath "$.data[0].output_tokens" == 7
jsonpath "$.data[0].charged_microcredits" == 45


GET {{base_url}}/account/v1/accounts/21000000-0000-4000-8000-000000000001/balance
Authorization: Bearer {{active_user_one_key}}

HTTP 200
[Asserts]
jsonpath "$.microcredits" == 99999907


GET {{fake_url}}/events

HTTP 200
[Asserts]
jsonpath "$.stream_calls" == 2
