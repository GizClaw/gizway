# Crash recovery — provider success before settlement is safely resumable.
#
# The companion process harness first sends this exact command while Gizway is
# configured to exit immediately after Bifrost returns a valid provider result
# and before gateway_settlement_outbox is inserted. It then restarts Gizway on
# the same PostgreSQL schema and runs this Hurl file.
#
# Durable requirements encoded here:
# - the background worker reclaims the expired execution lease without any
#   retry from the original HTTP client;
# - the encrypted attempt-one provider/credential/price snapshot is reused
#   even if Admin rotates the live endpoint credential before the retry;
# - Bifrost resends that ID as the provider Idempotency-Key;
# - the still-running provider replays its original success (one actual call);
# - one reservation becomes one exact 36-microcredit settlement;
# - the command is terminal and replayable after restart.
# protocol-case: openai.chat.provider-success-process-crash restart-recovery
# protocol covers: OpenAI POST /v1/chat/completions

# Rotate the live catalog credential after attempt one. Without an immutable
# execution snapshot, recovery would re-resolve the seeded endpoint at port 1,
# use this now-valid credential, and either fail or cross provider idempotency
# domains. Correct automatic recovery ignores this new catalog state and uses
# attempt one's default fake-provider target and exact price snapshot.
POST {{base_url}}/admin/v1/provider_endpoints/71000000-0000-4000-8000-000000000001/rotate_credential
Authorization: Bearer {{admin_api_key}}
Idempotency-Key: story-crash-rotate-live-credential
Content-Type: application/json
{
  "credential": "credential-valid-only-for-a-different-live-route"
}

HTTP 204


# Corrupt ciphertext is an infrastructure fault, so story mode exposes this
# narrow injection driver. Its row sorts ahead of the real crashed command and
# holds 7 microcredits. The worker must quarantine only that row as
# reconciliation_required, retain its reservation, and continue to recover the
# valid command behind it in the same batch.
POST {{base_url}}/test/v1/gateway-recovery/poison

HTTP 201
[Asserts]
jsonpath "$.gateway_request_id" == "0c000000-0000-4000-8000-000000000001"


# Process restart reuses the same deterministic fixture instant. Advancing the
# controlled clock beyond the one-second story lease makes the command eligible
# for the production recovery worker. No Gateway request is sent by this Hurl
# file: successful settlement below therefore proves no-client-retry recovery.
POST {{base_url}}/test/v1/clock/advance
Content-Type: application/json
{
  "by": "2s"
}

HTTP 200


# This read uses bounded assertion retry because the production worker is
# intentionally asynchronous. Polling is scoped to this observation and never
# replays any mutation in the file.
GET {{base_url}}/account/v1/accounts/21000000-0000-4000-8000-000000000001/balance
Authorization: Bearer {{active_user_one_key}}
[Options]
retry: 12
retry-interval: 250ms

HTTP 200
[Asserts]
jsonpath "$.microcredits" == 99999964


# Only after balance proves the worker has completed do we replay the original
# public command. This checks terminal response durability; it cannot be the
# action that recovered the expired lease.
POST {{base_url}}/v1/chat/completions
Authorization: Bearer {{gateway_api_key}}
Idempotency-Key: story-crash-after-provider-success
Content-Type: application/json
{
  "model": "story-text",
  "messages": [{"role": "user", "content": "recover after provider success"}],
  "stream": false
}

HTTP 200
[Asserts]
jsonpath "$.id" == "fake-chat-001"
jsonpath "$.model" == "story-text"
jsonpath "$.usage.prompt_tokens" == 10
jsonpath "$.usage.completion_tokens" == 5


GET {{fake_url}}/events

HTTP 200
[Asserts]
jsonpath "$.chat_calls" == 1


GET {{base_url}}/account/v1/accounts/21000000-0000-4000-8000-000000000001/usage?from=2026-08-10T00%3A00%3A00Z&to=2027-08-11T00%3A00%3A00Z
Authorization: Bearer {{active_user_one_key}}

HTTP 200
[Asserts]
jsonpath "$.data" count == 2
jsonpath "$.data[?(@.id == '0c000000-0000-4000-8000-000000000001')].charged_microcredits" == 0
jsonpath "$.data[?(@.id != '0c000000-0000-4000-8000-000000000001')].charged_microcredits" == 36


GET {{base_url}}/admin/v1/gateway_requests
Authorization: Bearer {{admin_api_key}}

HTTP 200
[Asserts]
jsonpath "$.data" count == 2
jsonpath "$.data[?(@.id == '0c000000-0000-4000-8000-000000000001')].status" == "started"
jsonpath "$.data[?(@.id == '0c000000-0000-4000-8000-000000000001')].recovery_status" == "reconciliation_required"
jsonpath "$.data[?(@.id == '0c000000-0000-4000-8000-000000000001')].recovery_attempts" == 1
jsonpath "$.data[?(@.id == '0c000000-0000-4000-8000-000000000001')].recovery_last_error" contains "decrypt recovery request"
jsonpath "$.data[?(@.id != '0c000000-0000-4000-8000-000000000001')].status" == "succeeded"
jsonpath "$.data[?(@.id != '0c000000-0000-4000-8000-000000000001')].charged_microcredits" == 36
jsonpath "$.data[?(@.id != '0c000000-0000-4000-8000-000000000001')].recovery_status" == "completed"
jsonpath "$.data[?(@.id != '0c000000-0000-4000-8000-000000000001')].recovery_last_error" == null
