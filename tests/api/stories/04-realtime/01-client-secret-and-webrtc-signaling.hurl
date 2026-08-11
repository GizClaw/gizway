# Story 04 — One-purpose Realtime credential and WebRTC signaling.
#
# Product behavior owned by this HTTP contract:
# - A Gateway key may mint a two-minute credential bound to one account, key,
#   canonical model, transport and database reservation.
# - Only SHA-256 of the secret is stored; plaintext is returned once.
# - Bifrost owns provider-specific WebRTC multipart signaling and keeps the
#   upstream credential out of the SDP/public response.
# - Because WebRTC media bypasses Gizway, the provider sends a signed terminal
#   usage event. Gizway journals it and settles measured usage exactly once:
#   input ceil(12*1800/1000)=22 plus output ceil(7*3600/1000)=26, total 48.
# - The credential is consumed by its first matching transport/session. Replay,
#   use on Account/Admin, or use for a different session fails before another
#   provider request.
#
# WebSocket frame relay, disconnect behavior and terminal usage settlement are
# owned by the adjacent Story 04 Hurl contracts. Since Hurl has no WebSocket
# frame primitive, those contracts call a fixture protocol driver which opens
# Gizway's real public socket and sends real frames; every business assertion
# is then made through Gizway's SQL-backed Account/Admin APIs.
#
# Fixture: story-base, Bifrost and the external OpenAI-compatible fake.
# Isolation: fresh PostgreSQL/processes per file.
# protocol-case: openai.realtime.websocket single-purpose-secret terminal-usage
# protocol-case: openai.realtime.webrtc sdp signed-callback single-settlement
# covers: listGatewayUsage getAccountBalance
# protocol covers: OpenAI POST /v1/realtime/client_secrets
# protocol covers: OpenAI POST /v1/realtime/calls WebRTC SDP

POST {{base_url}}/v1/realtime/client_secrets
Authorization: Bearer {{gateway_api_key}}
Idempotency-Key: story-04-webrtc-secret
Content-Type: application/json
{
  "model": "story-text",
  "transport": "webrtc"
}

HTTP 201
[Captures]
realtime_secret: jsonpath "$.client_secret.value"
realtime_session_id: jsonpath "$.session.session_id"
[Asserts]
jsonpath "$.client_secret.value" startsWith "gizrt_"
jsonpath "$.session.transport" == "webrtc"
jsonpath "$.session.status" == "created"
jsonpath "$.session.provider_model" not exists
jsonpath "$.session.model_variant_id" not exists


# The posted balance is not the spendable balance while this AI command owns a
# durable reservation. This transfer is below the posted 100,000,000 balance
# but above posted-minus-reserved, so it must fail before any ledger mutation.
POST {{base_url}}/account/v1/accounts/21000000-0000-4000-8000-000000000001/transfers
Authorization: Bearer {{active_user_one_key}}
Idempotency-Key: story-04-reserved-credit-cannot-transfer
Content-Type: application/json
{
  "recipient_account_id": "21000000-0000-4000-8000-000000000002",
  "amount": {"asset": "GIZ_CREDIT", "microcredits": 99980000},
  "note": "must not spend realtime reservation"
}

HTTP 409
[Asserts]
jsonpath "$.error.code" == "insufficient_balance"


POST {{base_url}}/v1/realtime/calls?session_id={{realtime_session_id}}
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


# Ask the external fixture to behave like the provider's independent billing
# webhook. Usage is provider-reported, never trusted from the client.
POST {{fake_url}}/complete-realtime
Content-Type: application/json
{
  "session_id": "{{realtime_session_id}}"
}

HTTP 200
[Asserts]
jsonpath "$.callback_status" == 200


# A provider retry is acknowledged but the started Gateway request prevents a
# second ledger charge.
POST {{fake_url}}/complete-realtime
Content-Type: application/json
{
  "session_id": "{{realtime_session_id}}"
}

HTTP 200
[Asserts]
jsonpath "$.callback_status" == 200


# Reusing the provider event id with a changed payload is not a retry. Gizway
# rejects it and preserves the original measured usage and charge.
POST {{fake_url}}/complete-realtime
Content-Type: application/json
{
  "session_id": "{{realtime_session_id}}",
  "input_tokens": 13
}

HTTP 409
[Asserts]
jsonpath "$.callback_status" == 409


# A second event ID for an already-terminal session is also invalid. There is
# exactly one durable provider terminal event per Realtime session; otherwise
# two distinct callbacks could both appear authoritative during a race.
POST {{fake_url}}/complete-realtime
Content-Type: application/json
{
  "session_id": "{{realtime_session_id}}",
  "event_id": "provider-webrtc-conflicting-second-event"
}

HTTP 409
[Asserts]
jsonpath "$.callback_status" == 409


# A single-purpose secret cannot signal the same session twice.
POST {{base_url}}/v1/realtime/calls?session_id={{realtime_session_id}}
Authorization: Bearer {{realtime_secret}}
Content-Type: application/sdp
```
v=0
o=replay 0 0 IN IP4 127.0.0.1
s=Replay Must Fail
t=0 0
```

HTTP 401


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
jsonpath "$.data[0].protocol" == "webrtc"
jsonpath "$.data[0].input_tokens" == 12
jsonpath "$.data[0].output_tokens" == 7
jsonpath "$.data[0].charged_microcredits" == 48


# Realtime credentials have no Account API authority.
GET {{base_url}}/account/v1/me
Authorization: Bearer {{realtime_secret}}

HTTP 401


GET {{fake_url}}/events

HTTP 200
[Asserts]
jsonpath "$.webrtc_calls" == 1
