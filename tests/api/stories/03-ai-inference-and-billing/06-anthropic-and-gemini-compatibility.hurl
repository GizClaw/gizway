# Story 03 — Anthropic Messages and Gemini GenerateContent are native contracts.
#
# Durable product requirements:
# - Clients use each ecosystem's ordinary credential location and wire shape:
#   Anthropic `x-api-key` with `/v1/messages`, Gemini `x-goog-api-key` with
#   `/v1beta/models/{model}:generateContent`. Those values authenticate only a
#   Gizway Gateway key; they are never forwarded as a provider credential.
# - Gizway-owned compatible codecs translate into Bifrost canonical Responses;
#   Bifrost alone performs the real provider execution; the result is converted
#   back into the selected public protocol rather than leaking OpenAI JSON.
# - Both calls use the same catalog variant and measured usage: ordinary input
#   9, cached input 2, output 6, and request 1. Each exact charge is 50
#   microcredits and each public response keeps the requested `story-text` ID.
# - Operation-scoped idempotency means an Anthropic key and a Gemini key may
#   share text safely without cross-protocol replay.
#
# Fixture/isolation: fresh PostgreSQL plus an independent external compatible fake
# per file. Only the upstream service is deterministic; database authorization,
# reservation, Bifrost HTTP, price snapshots, ledger and response storage are real.
# protocol-case: anthropic.messages.nonstream native-header native-response
# protocol-case: anthropic.messages.tools-vision preserved-tool-use
# protocol-case: gemini.generate-content.nonstream native-header native-response
# protocol-case: gemini.generate-content.tools-multimodal preserved-function-call
# protocol covers: Anthropic POST /v1/messages
# protocol covers: Gemini POST /v1beta/models/{model}:generateContent
# covers: getAccountBalance listGatewayUsage listAccountTransactions

POST {{base_url}}/v1/messages
x-api-key: {{gateway_api_key}}
anthropic-version: 2023-06-01
Idempotency-Key: story-03-anthropic-one
Content-Type: application/json
{
  "model": "story-text",
  "max_tokens": 64,
  "messages": [{"role": "user", "content": "reply through Anthropic Messages"}]
}

HTTP 200
[Asserts]
jsonpath "$.id" == "fake-response-001"
jsonpath "$.type" == "message"
jsonpath "$.role" == "assistant"
jsonpath "$.model" == "story-text"
jsonpath "$.content[0].type" == "text"
jsonpath "$.content[0].text" == "deterministic responses output"
# Anthropic reports cached reads separately, so ordinary input is 9 rather
# than the canonical total input 11 used for settlement.
jsonpath "$.usage.input_tokens" == 9
jsonpath "$.usage.cache_read_input_tokens" == 2
jsonpath "$.usage.output_tokens" == 6


POST {{base_url}}/v1beta/models/story-text:generateContent
x-goog-api-key: {{gateway_api_key}}
Idempotency-Key: story-03-gemini-one
Content-Type: application/json
{
  "contents": [{"role": "user", "parts": [{"text": "reply through Gemini GenerateContent"}]}],
  "generationConfig": {"maxOutputTokens": 64}
}

HTTP 200
[Asserts]
jsonpath "$.candidates" count == 1
jsonpath "$.candidates[0].content.role" == "model"
jsonpath "$.candidates[0].content.parts[0].text" == "deterministic responses output"
jsonpath "$.usageMetadata.promptTokenCount" == 11
jsonpath "$.usageMetadata.candidatesTokenCount" == 6
jsonpath "$.usageMetadata.totalTokenCount" == 17
jsonpath "$.modelVersion" == "story-text"


POST {{base_url}}/v1/messages
x-api-key: {{gateway_api_key}}
anthropic-version: 2023-06-01
Idempotency-Key: story-03-anthropic-tools-vision
Content-Type: application/json
{
  "model": "story-text",
  "max_tokens": 64,
  "tools": [{
    "name": "get_weather",
    "description": "Return deterministic weather",
    "input_schema": {
      "type": "object",
      "properties": {"city": {"type": "string"}},
      "required": ["city"]
    }
  }],
  "messages": [{
    "role": "user",
    "content": [
      {"type": "text", "text": "Use the tool after inspecting this image"},
      {"type": "image", "source": {"type": "base64", "media_type": "image/png", "data": "iVBORw0KGgo="}}
    ]
  }]
}

HTTP 200
[Asserts]
jsonpath "$.content[0].type" == "tool_use"
jsonpath "$.content[0].id" == "call_weather_001"
jsonpath "$.content[0].name" == "get_weather"
jsonpath "$.content[0].input.city" == "Shanghai"
jsonpath "$.stop_reason" == "tool_use"


POST {{base_url}}/v1beta/models/story-text:generateContent
x-goog-api-key: {{gateway_api_key}}
Idempotency-Key: story-03-gemini-tools-multimodal
Content-Type: application/json
{
  "contents": [{
    "role": "user",
    "parts": [
      {"text": "Use the tool after inspecting this image"},
      {"inlineData": {"mimeType": "image/png", "data": "iVBORw0KGgo="}}
    ]
  }],
  "tools": [{
    "functionDeclarations": [{
      "name": "get_weather",
      "description": "Return deterministic weather",
      "parameters": {
        "type": "object",
        "properties": {"city": {"type": "string"}},
        "required": ["city"]
      }
    }]
  }]
}

HTTP 200
[Asserts]
jsonpath "$.candidates[0].content.parts[0].functionCall.name" == "get_weather"
jsonpath "$.candidates[0].content.parts[0].functionCall.args.city" == "Shanghai"


GET {{fake_url}}/events

HTTP 200
[Asserts]
jsonpath "$.responses_calls" == 4


GET {{base_url}}/account/v1/accounts/21000000-0000-4000-8000-000000000001/balance
Authorization: Bearer {{active_user_one_key}}

HTTP 200
[Asserts]
jsonpath "$.microcredits" == 99999800


GET {{base_url}}/account/v1/accounts/21000000-0000-4000-8000-000000000001/usage?from=2026-08-10T00%3A00%3A00Z&to=2027-08-11T00%3A00%3A00Z
Authorization: Bearer {{active_user_one_key}}

HTTP 200
[Asserts]
jsonpath "$.data" count == 4
jsonpath "$.data[0].charged_microcredits" == 50
jsonpath "$.data[1].charged_microcredits" == 50
jsonpath "$.data[2].charged_microcredits" == 50
jsonpath "$.data[3].charged_microcredits" == 50


GET {{base_url}}/account/v1/accounts/21000000-0000-4000-8000-000000000001/transactions
Authorization: Bearer {{active_user_one_key}}

HTTP 200
[Asserts]
jsonpath "$.data" count == 5
jsonpath "$.data[0].amount.microcredits" == 50
jsonpath "$.data[1].amount.microcredits" == 50
