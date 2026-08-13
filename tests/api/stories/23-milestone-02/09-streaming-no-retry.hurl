# Milestone 02 — once Streaming output is visible, GizWay never retries another
# Provider Key, Provider, or Model.
# protocol-case: milestone02.openai.chat.streaming-partial no-retry

GET {{fake_url}}/events

HTTP 200
[Asserts]
jsonpath "$.chat_calls" == 0
jsonpath "$.stream_calls" == 0


# The fixture sends one real delta and then a terminal SSE error. This story's
# regional seed contains two active Keys for the Provider; a forbidden retry
# would therefore increment the Provider counters a second time.
POST {{way_url}}/v1/chat/completions
Authorization: Bearer {{raw_subscription_key}}
Content-Type: application/json
{
  "model":"story-text",
  "messages":[{"role":"user","content":"stream-error-after-first-delta"}],
  "stream":true,
  "stream_options":{"include_usage":true}
}

HTTP 200
[Asserts]
header "Content-Type" startsWith "text/event-stream"
body contains "deterministic"
body contains "fixture failed after first delta"
body not contains "[DONE]"


GET {{fake_url}}/events

HTTP 200
[Asserts]
jsonpath "$.chat_calls" == 1
jsonpath "$.stream_calls" == 1


# No terminal Usage means no AI Order and no Charge Outbox record.
GET {{way_url}}/admin/v1/ai-orders
Authorization: Bearer {{admin_token}}

HTTP 200
[Asserts]
jsonpath "$.data" count == 0


GET {{way_url}}/admin/v1/charge-outbox
Authorization: Bearer {{admin_token}}

HTTP 200
[Asserts]
jsonpath "$.data" count == 0
