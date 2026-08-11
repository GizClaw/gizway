# Story 03 — OpenAI advanced inputs are preserved through Bifrost.
#
# Durable product requirements:
# - Tool definitions and tool_choice are typed Chat parameters, not unknown
#   JSON silently discarded by Gizway. The provider's tool call and finish
#   reason survive the return conversion.
# - response_format reaches the provider and produces structured JSON content.
# - Multimodal content arrays are accepted as typed text/image parts; Gizway
#   never downloads the client image itself or rewrites it into plain text.
# - Each successful command still owns one reservation, one usage row and one
#   exact charge. Private Bifrost routing metadata never appears publicly.
#
# protocol-case: openai.chat.tools preserved-tool-call-and-finish-reason
# protocol-case: openai.chat.structured-output response-format-preserved
# protocol-case: openai.chat.multimodal typed-content-array
# protocol covers: OpenAI POST /v1/chat/completions

POST {{base_url}}/v1/chat/completions
Authorization: Bearer {{gateway_api_key}}
Idempotency-Key: story-03-chat-tool
Content-Type: application/json
{
  "model": "story-text",
  "messages": [{"role": "user", "content": "What is the weather?"}],
  "tools": [{
    "type": "function",
    "function": {
      "name": "get_weather",
      "description": "Return deterministic weather",
      "parameters": {
        "type": "object",
        "properties": {"city": {"type": "string"}},
        "required": ["city"],
        "additionalProperties": false
      }
    }
  }],
  "tool_choice": "auto",
  "stream": false
}

HTTP 200
[Asserts]
jsonpath "$.model" == "story-text"
jsonpath "$.choices[0].finish_reason" == "tool_calls"
jsonpath "$.choices[0].message.tool_calls[0].id" == "call_weather_001"
jsonpath "$.choices[0].message.tool_calls[0].function.name" == "get_weather"
jsonpath "$.choices[0].message.tool_calls[0].function.arguments" == "{\"city\":\"Shanghai\"}"
jsonpath "$.extra_fields" not exists


POST {{base_url}}/v1/chat/completions
Authorization: Bearer {{gateway_api_key}}
Idempotency-Key: story-03-chat-structured
Content-Type: application/json
{
  "model": "story-text",
  "messages": [{"role": "user", "content": "Return the answer as JSON"}],
  "response_format": {
    "type": "json_schema",
    "json_schema": {
      "name": "answer",
      "strict": true,
      "schema": {
        "type": "object",
        "properties": {"answer": {"type": "string"}},
        "required": ["answer"],
        "additionalProperties": false
      }
    }
  },
  "stream": false
}

HTTP 200
[Asserts]
jsonpath "$.choices[0].finish_reason" == "stop"
jsonpath "$.choices[0].message.content" == "{\"answer\":\"deterministic\"}"


POST {{base_url}}/v1/chat/completions
Authorization: Bearer {{gateway_api_key}}
Idempotency-Key: story-03-chat-multimodal
Content-Type: application/json
{
  "model": "story-text",
  "messages": [{
    "role": "user",
    "content": [
      {"type": "text", "text": "Describe this deterministic image"},
      {"type": "image_url", "image_url": {"url": "data:image/png;base64,iVBORw0KGgo="}}
    ]
  }],
  "stream": false
}

HTTP 200
[Asserts]
jsonpath "$.choices[0].message.content" == "deterministic story response"
jsonpath "$.usage.prompt_tokens" == 10


GET {{fake_url}}/events

HTTP 200
[Asserts]
jsonpath "$.chat_calls" == 3


GET {{base_url}}/account/v1/accounts/21000000-0000-4000-8000-000000000001/usage?from=2026-08-10T00%3A00%3A00Z&to=2027-08-11T00%3A00%3A00Z
Authorization: Bearer {{active_user_one_key}}

HTTP 200
[Asserts]
jsonpath "$.data" count == 3
jsonpath "$.data[0].charged_microcredits" == 36
jsonpath "$.data[1].charged_microcredits" == 36
jsonpath "$.data[2].charged_microcredits" == 36
