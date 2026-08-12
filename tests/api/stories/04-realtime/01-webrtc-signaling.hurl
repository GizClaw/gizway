# Story 04 — Realtime WebRTC uses one short-lived regional credential.
#
# The credential consumes only local Quota. Provider terminal Usage is queued
# regionally and is reported on a later Quota Exchange; no central reservation
# or synchronous ledger mutation is part of this protocol.
# protocol-case: openai.realtime.webrtc client-secret-sdp-terminal-usage
# protocol covers: OpenAI POST /v1/realtime/client_secrets
# protocol covers: OpenAI POST /v1/realtime/calls WebRTC SDP

POST {{way_url}}/v1/realtime/client_secrets
Authorization: Bearer {{gateway_api_key}}
Content-Type: application/json
{"model":"story-text","transport":"webrtc"}

HTTP 201
[Captures]
realtime_secret: jsonpath "$.client_secret.value"
realtime_session_id: jsonpath "$.session.session_id"
[Asserts]
jsonpath "$.client_secret.value" startsWith "gizrt_"
jsonpath "$.session.transport" == "webrtc"


POST {{way_url}}/v1/realtime/calls?session_id={{realtime_session_id}}
Authorization: Bearer {{realtime_secret}}
Content-Type: application/sdp
```
v=0
o=story-offer 0 0 IN IP4 127.0.0.1
s=Gizway Story Offer
t=0 0
```

HTTP 201
[Asserts]
header "Content-Type" startsWith "application/sdp"
body contains "Gizway Fake Answer"


POST {{fake_url}}/complete-realtime
Content-Type: application/json
{"session_id":"{{realtime_session_id}}"}

HTTP 200
[Asserts]
jsonpath "$.callback_status" == 200


GET {{way_url}}/admin/v1/gateway_executions/{{realtime_session_id}}
Authorization: Bearer {{admin_api_key}}

HTTP 200
[Asserts]
jsonpath "$.status" == "completed"
jsonpath "$.protocol" == "webrtc"
