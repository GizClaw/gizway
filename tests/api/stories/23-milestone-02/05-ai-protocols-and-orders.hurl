# Milestone 02 — one regional GizWay exposes all regional PAYG models.
# protocol-case: milestone02.openai.chat.nonstream payg-charge
# protocol-case: milestone02.anthropic.messages.nonstream payg-charge
# protocol-case: milestone02.gemini.generate.nonstream payg-charge
# protocol-case: milestone02.openai.chat.streaming payg-charge
# protocol-case: milestone02.openai.realtime.websocket payg-charge
# protocol covers: OpenAI POST /v1/chat/completions
# protocol covers: OpenAI POST /v1/realtime/client_secrets
# protocol covers: OpenAI GET /v1/realtime WebSocket
# protocol covers: Anthropic POST /v1/messages
# protocol covers: Gemini POST /v1beta/models/{model}:generateContent
# covers: gizway-admin.yaml#listAIOrders gizway-admin.yaml#listChargeOutbox gizway-admin.yaml#listBifrostLogs
# covers: gizway-public.yaml#listModels gizway-public.yaml#createChatCompletion gizway-public.yaml#createAnthropicMessage
# covers: gizway-public.yaml#generateGeminiContent gizway-public.yaml#createRealtimeClientSecret gizway-public.yaml#connectRealtimeWebSocket

GET {{way_url}}/v1/models
Authorization: Bearer {{raw_subscription_key}}

HTTP 200
[Asserts]
jsonpath "$.data[?(@.id == 'story-text')]" count == 1
jsonpath "$.data[?(@.id == 'story-text')].object" contains "model"


# Milestone 02 does not apply a Product-to-Model policy. A Key belonging to a
# different PAYG Product sees the same regional public catalog.
GET {{way_url}}/v1/models
Authorization: Bearer {{raw_subscription_key_other_product}}

HTTP 200
[Asserts]
jsonpath "$.data[?(@.id == 'story-text')]" count == 1


POST {{way_url}}/v1/chat/completions
Authorization: Bearer {{raw_subscription_key}}
Content-Type: application/json
{"model":"story-text","messages":[{"role":"user","content":"hello"}],"stream":false}

HTTP 200
[Asserts]
jsonpath "$.object" == "chat.completion"
jsonpath "$.model" == "story-text"


POST {{way_url}}/v1/messages
Authorization: Bearer {{raw_subscription_key}}
anthropic-version: 2023-06-01
Content-Type: application/json
{"model":"story-text","max_tokens":64,"messages":[{"role":"user","content":"hello"}]}

HTTP 200
[Asserts]
jsonpath "$.type" == "message"
jsonpath "$.model" == "story-text"
jsonpath "$.content[0].text" == "deterministic story response"


POST {{way_url}}/v1beta/models/story-text:generateContent
x-goog-api-key: {{raw_subscription_key}}
Content-Type: application/json
{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}

HTTP 200
[Asserts]
jsonpath "$.candidates[0].content.parts[0].text" exists
jsonpath "$.candidates[0].content.parts[0].text" == "deterministic story response"


# Streaming uses terminal provider Usage. For the fixture prices, 12 input and
# 7 output tokens round up independently to Gross 7 and Commission 4.
POST {{way_url}}/v1/chat/completions
Authorization: Bearer {{raw_subscription_key}}
Content-Type: application/json
{
  "model":"story-text",
  "messages":[{"role":"user","content":"streaming"}],
  "stream":true,
  "stream_options":{"include_usage":true}
}

HTTP 200
[Asserts]
header "Content-Type" startsWith "text/event-stream"
body contains "deterministic "
body contains "stream response"
body contains "[DONE]"


# Hurl cannot originate WebSocket frames, so the deterministic Fake Provider
# drives the real public socket and waits for terminal provider Usage. This
# direct request still binds the OpenAPI operation and rejects a bad session.
GET {{way_url}}/v1/realtime?session_id=invalid
Authorization: Bearer invalid

HTTP 401


POST {{way_url}}/v1/realtime/client_secrets
Authorization: Bearer {{raw_subscription_key}}
Content-Type: application/json
{"model":"story-text","transport":"websocket"}

HTTP 201
[Captures]
realtime_secret: jsonpath "$.client_secret.value"
realtime_session_id: jsonpath "$.session.session_id"
[Asserts]
jsonpath "$.session.transport" == "websocket"


POST {{fake_url}}/drive-realtime
Content-Type: application/json
{
  "api_url":"{{way_url}}",
  "secret":"{{realtime_secret}}",
  "session_id":"{{realtime_session_id}}",
  "scenario":"text_audio"
}

HTTP 200
[Asserts]
jsonpath "$.driven" == true


GET {{way_url}}/admin/v1/ai-orders
Authorization: Bearer {{admin_token}}
[Options]
retry: 20
retry-interval: 50ms

HTTP 200
[Asserts]
jsonpath "$.data" count == 5
# Non-stream OpenAI, Anthropic, and Gemini each normalize to 10 input and 5
# output tokens: ceil(10*3/10) + ceil(5*3/10) = Gross 5.
jsonpath "$.data[?(@.gross_microcredits == 5)]" count == 3
jsonpath "$.data[?(@.commission_microcredits == 3)]" count == 3
# Streaming and Realtime both use the terminal 12/7 usage: Gross 7.
jsonpath "$.data[?(@.gross_microcredits == 7)]" count == 2
jsonpath "$.data[?(@.commission_microcredits == 4)]" count == 2
body not contains "{{raw_subscription_key}}"
body not contains "{{provider_key_secret}}"


GET {{way_url}}/admin/v1/charge-outbox
Authorization: Bearer {{admin_token}}
[Options]
retry: 20
retry-interval: 50ms

HTTP 200
[Asserts]
jsonpath "$.data" count == 5
jsonpath "$.data[?(@.status == 'reported')]" count == 5
jsonpath "$.data[?(@.payload.gross_microcredits == 5)]" count == 3
jsonpath "$.data[?(@.payload.gross_microcredits == 7)]" count == 2
body not contains "{{raw_subscription_key}}"
body not contains "{{provider_key_secret}}"


GET {{way_url}}/admin/v1/bifrost-logs
Authorization: Bearer {{admin_token}}

HTTP 200
[Asserts]
jsonpath "$.data" count == 5
jsonpath "$.data[*].selected_key_id" count == 5


# Zero prices are valid catalog data. The call and Bifrost log still exist, but
# Gross is zero so GizWay must not create an Outbox row or call Charge.
PUT {{way_url}}/admin/v1/models/{{seeded_model_id}}/prices
Authorization: Bearer {{admin_token}}
Content-Type: application/json
{
  "prices": [
    {"metric":"input_token","unit_size":10,"price_microcredits":0},
    {"metric":"output_token","unit_size":10,"price_microcredits":0}
  ]
}

HTTP 200


PUT {{way_url}}/admin/v1/provider-api-keys/{{seeded_bifrost_key_id}}/prices
Authorization: Bearer {{admin_token}}
Content-Type: application/json
{
  "prices": [
    {"model_id":"{{seeded_model_id}}","metric":"input_token","unit_size":10,"commission_microcredits":0},
    {"model_id":"{{seeded_model_id}}","metric":"output_token","unit_size":10,"commission_microcredits":0}
  ]
}

HTTP 200


POST {{way_url}}/v1/chat/completions
Authorization: Bearer {{raw_subscription_key}}
Content-Type: application/json
{"model":"story-text","messages":[{"role":"user","content":"free call"}],"stream":false}

HTTP 200


GET {{way_url}}/admin/v1/ai-orders
Authorization: Bearer {{admin_token}}
[Options]
retry: 20
retry-interval: 50ms

HTTP 200
[Asserts]
jsonpath "$.data" count == 6
jsonpath "$.data[?(@.gross_microcredits == 0)]" count == 1
jsonpath "$.data[?(@.commission_microcredits == 0)]" count == 1


GET {{way_url}}/admin/v1/charge-outbox
Authorization: Bearer {{admin_token}}

HTTP 200
[Asserts]
jsonpath "$.data" count == 5
