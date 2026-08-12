# Story 10 — PowerSync uses a signed user subject, never a client account ID.
#
# Product behavior retained by this executable specification:
# - An authenticated User session can obtain a short-lived PowerSync JWT.
# - The JWT lifetime is bounded and the token is returned only in this response.
# - The sync-rule parameter query derives active account membership from the
#   replicated accounts table using the signed JWT `sub` claim.
# - A client cannot request another user's account projection by changing a
#   connection parameter or account ID.
# - Only the five read-only account projections are visible; provider secrets,
#   API-key hashes, callback payloads and raw ledger entries are not projected.
#
# The `/test/v1/powersync/authorize` route is registered only by story mode. It
# is a driver for the external PowerSync boundary: it verifies the real HS256
# token, resolves the signed subject through the real sqlx Store, and queries
# the same five database views named by data/powersync/sync-rules.yaml.
#
# Fixture: story-base
# Isolation: fresh PostgreSQL database and process for this file.
# covers: account.yaml#createPowerSyncCredentials

POST {{pay_url}}/account/v1/powersync/credentials
Authorization: Bearer {{active_user_one_key}}

HTTP 200
[Captures]
powersync_token: jsonpath "$.token"
[Asserts]
jsonpath "$.endpoint" == "https://sync.gizway.test"
jsonpath "$.token" matches "^[A-Za-z0-9_-]+\\.[A-Za-z0-9_-]+\\.[A-Za-z0-9_-]+$"
jsonpath "$.expires_at" == "2026-08-12T12:05:00.000000000Z"


# The signed subject can observe its own account-keyed projections.
POST {{pay_url}}/test/v1/powersync/authorize
Authorization: Bearer {{powersync_token}}
Content-Type: application/json
{
  "account_id": "21000000-0000-4000-8000-000000000001"
}

HTTP 200
[Asserts]
jsonpath "$.account_id" == "21000000-0000-4000-8000-000000000001"
jsonpath "$.projection_counts.powersync_account_balances" == 1
jsonpath "$.projection_counts.powersync_gateway_usage" == 0


# Changing only account_id cannot widen the signed subject's buckets.
POST {{pay_url}}/test/v1/powersync/authorize
Authorization: Bearer {{powersync_token}}
Content-Type: application/json
{
  "account_id": "21000000-0000-4000-8000-000000000002"
}

HTTP 403
[Asserts]
jsonpath "$.error.code" == "forbidden"


# JWT integrity is checked before any projection lookup.
POST {{pay_url}}/test/v1/powersync/authorize
Authorization: Bearer {{powersync_token}}tampered
Content-Type: application/json
{
  "account_id": "21000000-0000-4000-8000-000000000001"
}

HTTP 401
[Asserts]
jsonpath "$.error.code" == "unauthorized"
