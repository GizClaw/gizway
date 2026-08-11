# Story 03 — OpenAI, Anthropic and Gemini streams share one settlement contract.
#
# Durable product requirements:
# - All three routes invoke Bifrost's true Responses streaming method. Gizway
#   forwards provider deltas as they arrive and never buffers a non-streaming
#   response merely to imitate SSE.
# - The OpenAI stream exposes Responses event names; Anthropic exposes native
#   `message_*`/`content_block_*` frames; Gemini exposes native
#   GenerateContent chunks. Public frames never expose the private upstream
#   model, endpoint, or credential.
# - Only terminal provider usage is authoritative. Each stream bills ordinary
#   input 9, cached input 2, output 6, and request 1 for exactly 50
#   microcredits. Deltas do not create partial ledger postings.
# - Exact retry replays stored framed bytes and cannot invoke Bifrost/provider
#   or settle a second time. Client disconnect/error paths release reservation.
#
# Fixture/isolation: one fresh PostgreSQL database and one external streaming fake
# are used by this file. The fake emits OpenAI Responses SSE; the Anthropic and
# Gemini frames are produced by their pinned Bifrost compatibility codecs.
# protocol-case: openai.responses.streaming terminal-usage replay
# protocol-case: anthropic.messages.streaming native-sse terminal-usage
# protocol-case: gemini.stream-generate-content.streaming native-sse terminal-usage
# protocol covers: OpenAI streaming POST /v1/responses
# protocol covers: Anthropic streaming POST /v1/messages
# protocol covers: Gemini streaming POST /v1beta/models/{model}:streamGenerateContent
# covers: getAccountBalance listGatewayUsage listAccountTransactions

POST {{base_url}}/v1/responses
Authorization: Bearer {{gateway_api_key}}
Idempotency-Key: story-03-responses-stream
Content-Type: application/json
{
  "model": "story-text",
  "input": "stream through OpenAI Responses",
  "stream": true,
  "max_output_tokens": 64
}

HTTP 200
[Asserts]
header "Content-Type" startsWith "text/event-stream"
body contains "event: response.created"
body contains "event: response.output_text.delta"
body contains "deterministic responses output"
body contains "event: response.completed"
body contains "\"model\":\"story-text\""


# Replayed frames are byte-persisted public protocol output.
POST {{base_url}}/v1/responses
Authorization: Bearer {{gateway_api_key}}
Idempotency-Key: story-03-responses-stream
Content-Type: application/json
{
  "model": "story-text",
  "input": "stream through OpenAI Responses",
  "stream": true,
  "max_output_tokens": 64
}

HTTP 200
[Asserts]
body contains "fake-response-001"
body contains "event: response.completed"


POST {{base_url}}/v1/messages
x-api-key: {{gateway_api_key}}
anthropic-version: 2023-06-01
Idempotency-Key: story-03-anthropic-stream
Content-Type: application/json
{
  "model": "story-text",
  "max_tokens": 64,
  "stream": true,
  "messages": [{"role": "user", "content": "stream through Anthropic"}]
}

HTTP 200
[Asserts]
header "Content-Type" startsWith "text/event-stream"
body contains "event: message_start"
body contains "deterministic responses output"
body contains "event: message_stop"


POST {{base_url}}/v1beta/models/story-text:streamGenerateContent?alt=sse
x-goog-api-key: {{gateway_api_key}}
Idempotency-Key: story-03-gemini-stream
Content-Type: application/json
{
  "contents": [{"role": "user", "parts": [{"text": "stream through Gemini"}]}],
  "generationConfig": {"maxOutputTokens": 64}
}

HTTP 200
[Asserts]
header "Content-Type" startsWith "text/event-stream"
body contains "data: {"
body contains "deterministic responses output"
body contains "\"modelVersion\":\"story-text\""


GET {{fake_url}}/events

HTTP 200
[Asserts]
jsonpath "$.responses_calls" == 3


GET {{base_url}}/account/v1/accounts/21000000-0000-4000-8000-000000000001/balance
Authorization: Bearer {{active_user_one_key}}

HTTP 200
[Asserts]
jsonpath "$.microcredits" == 99999850


GET {{base_url}}/account/v1/accounts/21000000-0000-4000-8000-000000000001/usage?from=2026-08-10T00%3A00%3A00Z&to=2027-08-11T00%3A00%3A00Z
Authorization: Bearer {{active_user_one_key}}

HTTP 200
[Asserts]
jsonpath "$.data" count == 3
jsonpath "$.data[0].charged_microcredits" == 50
jsonpath "$.data[1].charged_microcredits" == 50
jsonpath "$.data[2].charged_microcredits" == 50


GET {{base_url}}/account/v1/accounts/21000000-0000-4000-8000-000000000001/transactions
Authorization: Bearer {{active_user_one_key}}

HTTP 200
[Asserts]
jsonpath "$.data" count == 4
jsonpath "$.data[0].amount.microcredits" == 50
jsonpath "$.data[1].amount.microcredits" == 50
jsonpath "$.data[2].amount.microcredits" == 50
