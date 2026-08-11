# Story 04 — WebSocket Realtime lifecycle, terminal usage and capacity limits.
#
# Hurl does not implement WebSocket frames. The fake `/drive-realtime` endpoint
# is therefore a protocol driver, not a business mock: it dials Gizway's public
# WebSocket URL with the real one-purpose secret and exchanges real frames.
# Every acceptance assertion below is read back from Gizway's SQL-backed APIs.
# This keeps the complete business contract in Hurl.
#
# Product behavior owned here:
# - a text+audio turn relays both modalities and settles only the provider's
#   trusted terminal usage. Audio is a priced subset, never double-counted:
#   text input ceil(6*1800/1000)=11, cached input ceil(2*900/1000)=2,
#   audio input ceil(4*2700/1000)=11,
#   text output ceil(5*3600/1000)=18, audio output
#   ceil(2*5400/1000)=11, for exactly 53 microcredits;
# - disconnect before terminal usage releases the complete reservation and
#   charges zero, whether the client or provider disconnects;
# - an idle connected session reaches the same durable absolute deadline in
#   the live socket and database worker, then releases with `session_timeout`;
# - one key can own one active Realtime session and one account can own two
#   sessions across distinct keys. Capacity rejection happens before a new
#   reservation/provider connection.
#
# Fixture: fresh story-base PostgreSQL, Gizway, Bifrost and external fake provider.
# Isolation: this file receives fresh processes/database and owns all IDs below.
# protocol-case: openai.realtime.websocket.text-audio terminal-usage
# protocol-case: openai.realtime.websocket.client-disconnect reservation-release
# protocol-case: openai.realtime.websocket.provider-disconnect reservation-release
# protocol-case: openai.realtime.websocket.timeout session-limits


# --- Successful text + audio turn -------------------------------------------

POST {{base_url}}/v1/realtime/client_secrets
Authorization: Bearer {{gateway_api_key}}
Idempotency-Key: story-04-ws-success-secret
Content-Type: application/json
{
  "model": "story-text",
  "transport": "websocket"
}

HTTP 201
[Captures]
success_secret: jsonpath "$.client_secret.value"
success_session_id: jsonpath "$.session.session_id"
[Asserts]
jsonpath "$.session.status" == "created"
jsonpath "$.session.transport" == "websocket"


POST {{fake_url}}/drive-realtime
Content-Type: application/json
{
  "api_url": "{{base_url}}",
  "secret": "{{success_secret}}",
  "session_id": "{{success_session_id}}",
  "scenario": "text_audio"
}

HTTP 200
[Asserts]
jsonpath "$.driven" == true


GET {{base_url}}/admin/v1/gateway_requests/{{success_session_id}}
Authorization: Bearer {{admin_api_key}}
[Options]
retry: 20
retry-interval: 25ms

HTTP 200
[Asserts]
jsonpath "$.status" == "succeeded"
jsonpath "$.protocol" == "websocket"
# As in every Gateway usage row, input_tokens excludes the separately exposed
# cached subset: 6 ordinary text + 4 audio = 10; adding cached_input_tokens
# reconstructs the provider's aggregate 12.
jsonpath "$.input_tokens" == 10
jsonpath "$.output_tokens" == 7
jsonpath "$.cached_input_tokens" == 2
jsonpath "$.input_audio_tokens" == 4
jsonpath "$.output_audio_tokens" == 2
jsonpath "$.charged_microcredits" == 53


# The Account usage projection is the customer-visible immutable price
# snapshot. It proves each modality used its own metric and that text
# quantities exclude the audio-token subsets.
GET {{base_url}}/account/v1/accounts/21000000-0000-4000-8000-000000000001/usage?from=2026-08-10T00%3A00%3A00Z&to=2027-08-11T00%3A00%3A00Z
Authorization: Bearer {{active_user_one_key}}

HTTP 200
[Asserts]
jsonpath "$.data" count == 1
jsonpath "$.data[0].input_tokens" == 10
jsonpath "$.data[0].output_tokens" == 7
jsonpath "$.data[0].cached_input_tokens" == 2
jsonpath "$.data[0].input_audio_tokens" == 4
jsonpath "$.data[0].output_audio_tokens" == 2
jsonpath "$.data[0].charges" count == 5
jsonpath "$.data[0].charges[0].metric" == "cached_input_token"
jsonpath "$.data[0].charges[0].quantity" == 2
jsonpath "$.data[0].charges[0].charged_microcredits" == 2
jsonpath "$.data[0].charges[1].metric" == "input_audio_token"
jsonpath "$.data[0].charges[1].quantity" == 4
jsonpath "$.data[0].charges[1].charged_microcredits" == 11
jsonpath "$.data[0].charges[2].metric" == "input_token"
jsonpath "$.data[0].charges[2].quantity" == 6
jsonpath "$.data[0].charges[2].charged_microcredits" == 11
jsonpath "$.data[0].charges[3].metric" == "output_audio_token"
jsonpath "$.data[0].charges[3].quantity" == 2
jsonpath "$.data[0].charges[3].charged_microcredits" == 11
jsonpath "$.data[0].charges[4].metric" == "output_token"
jsonpath "$.data[0].charges[4].quantity" == 5
jsonpath "$.data[0].charges[4].charged_microcredits" == 18


GET {{base_url}}/account/v1/accounts/21000000-0000-4000-8000-000000000001/balance
Authorization: Bearer {{active_user_one_key}}

HTTP 200
[Asserts]
jsonpath "$.microcredits" == 99999947


GET {{fake_url}}/events

HTTP 200
[Asserts]
jsonpath "$.realtime_calls" == 1
jsonpath "$.realtime_audio_events" == 1


# --- Client disconnect: no trusted terminal usage, therefore zero charge ----

POST {{base_url}}/v1/realtime/client_secrets
Authorization: Bearer {{gateway_api_key}}
Idempotency-Key: story-04-ws-client-disconnect-secret
Content-Type: application/json
{
  "model": "story-text",
  "transport": "websocket"
}

HTTP 201
[Captures]
client_disconnect_secret: jsonpath "$.client_secret.value"
client_disconnect_session_id: jsonpath "$.session.session_id"


POST {{fake_url}}/drive-realtime
Content-Type: application/json
{
  "api_url": "{{base_url}}",
  "secret": "{{client_disconnect_secret}}",
  "session_id": "{{client_disconnect_session_id}}",
  "scenario": "client_disconnect"
}

HTTP 200


GET {{base_url}}/admin/v1/gateway_requests/{{client_disconnect_session_id}}
Authorization: Bearer {{admin_api_key}}
[Options]
retry: 20
retry-interval: 25ms

HTTP 200
[Asserts]
jsonpath "$.status" == "failed"
jsonpath "$.error_code" == "client_disconnect"
jsonpath "$.charged_microcredits" == 0


# --- Provider disconnect: same partial-session release policy ----------------

POST {{base_url}}/v1/realtime/client_secrets
Authorization: Bearer {{gateway_api_key}}
Idempotency-Key: story-04-ws-provider-disconnect-secret
Content-Type: application/json
{
  "model": "story-text",
  "transport": "websocket"
}

HTTP 201
[Captures]
provider_disconnect_secret: jsonpath "$.client_secret.value"
provider_disconnect_session_id: jsonpath "$.session.session_id"


POST {{fake_url}}/drive-realtime
Content-Type: application/json
{
  "api_url": "{{base_url}}",
  "secret": "{{provider_disconnect_secret}}",
  "session_id": "{{provider_disconnect_session_id}}",
  "scenario": "provider_disconnect"
}

HTTP 200


GET {{base_url}}/admin/v1/gateway_requests/{{provider_disconnect_session_id}}
Authorization: Bearer {{admin_api_key}}
[Options]
retry: 20
retry-interval: 25ms

HTTP 200
[Asserts]
jsonpath "$.status" == "failed"
jsonpath "$.error_code" == "provider_disconnect"
jsonpath "$.charged_microcredits" == 0


# --- Idle timeout: live socket and durable database use one deadline ----------

POST {{base_url}}/v1/realtime/client_secrets
Authorization: Bearer {{gateway_api_key}}
Idempotency-Key: story-04-ws-timeout-secret
Content-Type: application/json
{
  "model": "story-text",
  "transport": "websocket"
}

HTTP 201
[Captures]
timeout_secret: jsonpath "$.client_secret.value"
timeout_session_id: jsonpath "$.session.session_id"


POST {{fake_url}}/drive-realtime
Content-Type: application/json
{
  "api_url": "{{base_url}}",
  "secret": "{{timeout_secret}}",
  "session_id": "{{timeout_session_id}}",
  "scenario": "timeout"
}

HTTP 200


GET {{base_url}}/admin/v1/gateway_requests/{{timeout_session_id}}
Authorization: Bearer {{admin_api_key}}
[Options]
retry: 20
retry-interval: 25ms

HTTP 200
[Asserts]
jsonpath "$.status" == "failed"
jsonpath "$.error_code" == "session_timeout"
jsonpath "$.charged_microcredits" == 0


# Failed/partial sessions did not debit the account.
GET {{base_url}}/account/v1/accounts/21000000-0000-4000-8000-000000000001/balance
Authorization: Bearer {{active_user_one_key}}

HTTP 200
[Asserts]
jsonpath "$.microcredits" == 99999947


# --- Concurrent-session limits ----------------------------------------------

# Create a second Gateway key for the same account. Its plaintext is returned
# once and is used only inside this isolated story.
POST {{base_url}}/account/v1/accounts/21000000-0000-4000-8000-000000000001/api_keys
Authorization: Bearer {{active_user_one_key}}
Idempotency-Key: story-04-second-realtime-key
Content-Type: application/json
{
  "name": "Story 04 second Realtime key",
  "kind": "gateway",
  "scopes": ["account:self", "gateway:invoke", "gateway:usage:read"]
}

HTTP 201
[Captures]
second_gateway_key: jsonpath "$.secret"


POST {{base_url}}/account/v1/accounts/21000000-0000-4000-8000-000000000001/api_keys
Authorization: Bearer {{active_user_one_key}}
Idempotency-Key: story-04-third-realtime-key
Content-Type: application/json
{
  "name": "Story 04 third Realtime key",
  "kind": "gateway",
  "scopes": ["account:self", "gateway:invoke", "gateway:usage:read"]
}

HTTP 201
[Captures]
third_gateway_key: jsonpath "$.secret"


# First active session occupies the seeded key's per-key slot.
POST {{base_url}}/v1/realtime/client_secrets
Authorization: Bearer {{gateway_api_key}}
Idempotency-Key: story-04-limit-primary
Content-Type: application/json
{
  "model": "story-text",
  "transport": "websocket"
}

HTTP 201
[Captures]
limit_primary_secret: jsonpath "$.client_secret.value"
limit_primary_session_id: jsonpath "$.session.session_id"


# Same key cannot reserve a second active session.
POST {{base_url}}/v1/realtime/client_secrets
Authorization: Bearer {{gateway_api_key}}
Idempotency-Key: story-04-limit-same-key-rejected
Content-Type: application/json
{
  "model": "story-text",
  "transport": "websocket"
}

HTTP 429
[Asserts]
jsonpath "$.error.code" == "session_limit"


# A distinct key may occupy the account's second slot even with the same
# Idempotency-Key text. Command identity is scoped to the authenticated key,
# so one application cannot replay or conflict with another application's
# response merely because both belong to the same account.
POST {{base_url}}/v1/realtime/client_secrets
Authorization: Bearer {{second_gateway_key}}
Idempotency-Key: story-04-limit-primary
Content-Type: application/json
{
  "model": "story-text",
  "transport": "websocket"
}

HTTP 201
[Captures]
limit_secondary_secret: jsonpath "$.client_secret.value"
limit_secondary_session_id: jsonpath "$.session.session_id"


# A third distinct key is valid, but the account already owns two active
# sessions. The account-level limit rejects it before creating a reservation.
POST {{base_url}}/v1/realtime/client_secrets
Authorization: Bearer {{third_gateway_key}}
Idempotency-Key: story-04-limit-account-rejected
Content-Type: application/json
{
  "model": "story-text",
  "transport": "websocket"
}

HTTP 429
[Asserts]
jsonpath "$.error.code" == "session_limit"


# Cleanly drive both active sessions through the zero-charge disconnect policy,
# proving capacity is eventually released rather than leaked.
POST {{fake_url}}/drive-realtime
Content-Type: application/json
{
  "api_url": "{{base_url}}",
  "secret": "{{limit_primary_secret}}",
  "session_id": "{{limit_primary_session_id}}",
  "scenario": "client_disconnect"
}

HTTP 200


POST {{fake_url}}/drive-realtime
Content-Type: application/json
{
  "api_url": "{{base_url}}",
  "secret": "{{limit_secondary_secret}}",
  "session_id": "{{limit_secondary_session_id}}",
  "scenario": "provider_disconnect"
}

HTTP 200


GET {{base_url}}/admin/v1/gateway_requests/{{limit_primary_session_id}}
Authorization: Bearer {{admin_api_key}}
[Options]
retry: 20
retry-interval: 25ms

HTTP 200
[Asserts]
jsonpath "$.status" == "failed"
jsonpath "$.error_code" == "client_disconnect"


GET {{base_url}}/admin/v1/gateway_requests/{{limit_secondary_session_id}}
Authorization: Bearer {{admin_api_key}}
[Options]
retry: 20
retry-interval: 25ms

HTTP 200
[Asserts]
jsonpath "$.status" == "failed"
jsonpath "$.error_code" == "provider_disconnect"
