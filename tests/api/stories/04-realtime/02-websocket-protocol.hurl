# Story 04 — Realtime WebSocket relays actual frames through the fake driver.
#
# Hurl cannot originate WebSocket frames, so the provider fixture drives the
# public socket. The final assertion reads GizWay's regional execution record.
# protocol-case: openai.realtime.websocket text-audio-terminal-usage
# protocol covers: OpenAI GET /v1/realtime WebSocket

POST {{way_url}}/v1/realtime/client_secrets
Authorization: Bearer {{gateway_api_key}}
Content-Type: application/json
{"model":"story-text","transport":"websocket"}

HTTP 201
[Captures]
websocket_secret: jsonpath "$.client_secret.value"
websocket_session_id: jsonpath "$.session.session_id"


POST {{fake_url}}/drive-realtime
Content-Type: application/json
{
  "api_url":"{{way_url}}",
  "secret":"{{websocket_secret}}",
  "session_id":"{{websocket_session_id}}",
  "scenario":"text_audio"
}

HTTP 200
[Asserts]
jsonpath "$.driven" == true


GET {{way_url}}/admin/v1/gateway_executions/{{websocket_session_id}}
Authorization: Bearer {{admin_api_key}}
[Options]
retry: 20
retry-interval: 25ms

HTTP 200
[Asserts]
jsonpath "$.status" == "completed"
jsonpath "$.protocol" == "websocket"
