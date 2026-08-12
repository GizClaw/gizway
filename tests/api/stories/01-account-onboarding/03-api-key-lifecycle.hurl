# Story 01 — Create, list, use, and revoke an independently attributable key.
#
# Product behavior:
# - The authenticated user may manage keys only for an owned account.
# - The create response is the only representation containing the plaintext.
# - List responses expose metadata and prefix, never the secret or secret_hash.
# - Revocation is irreversible and immediately prevents authentication.
# - Unsupported scopes and unknown JSON fields fail before a row is created.
#
# Fixture: story-base
# Isolation: this file receives a new PostgreSQL schema and server process.
# covers: account.yaml#createApiKey account.yaml#listApiKeys account.yaml#revokeApiKey account.yaml#getCurrentUser

# Capture the baseline count so later assertions prove failed commands had no
# database side effect. story-base contains one bootstrap key for this account.
GET {{pay_url}}/account/v1/accounts/21000000-0000-4000-8000-000000000001/api_keys
Authorization: Bearer {{active_user_one_key}}

HTTP 200
[Asserts]
jsonpath "$.data" count == 1
jsonpath "$.data[0].secret" not exists
jsonpath "$.data[0].secret_hash" not exists


# Unknown fields are rejected instead of being silently ignored.
POST {{pay_url}}/account/v1/accounts/21000000-0000-4000-8000-000000000001/api_keys
Authorization: Bearer {{active_user_one_key}}
Idempotency-Key: story-01-reject-unknown-field
Content-Type: application/json
{
  "name": "Invalid unknown field",
  "kind": "gateway",
  "scopes": ["gateway:invoke"],
  "unexpected": true
}

HTTP 400
[Asserts]
jsonpath "$.error.code" == "invalid_request"


# Scope allowlists are enforced for the selected credential kind.
POST {{pay_url}}/account/v1/accounts/21000000-0000-4000-8000-000000000001/api_keys
Authorization: Bearer {{active_user_one_key}}
Idempotency-Key: story-01-reject-payment-scope
Content-Type: application/json
{
  "name": "Invalid gateway scope",
  "kind": "gateway",
  "scopes": ["pay:intents:write"]
}

HTTP 400
[Asserts]
jsonpath "$.error.code" == "invalid_request"


# A successful creation returns a new unpredictable plaintext exactly where it
# can be captured by the client. Subsequent representations expose only prefix.
POST {{pay_url}}/account/v1/accounts/21000000-0000-4000-8000-000000000001/api_keys
Authorization: Bearer {{active_user_one_key}}
Idempotency-Key: story-01-create-gateway-key
Content-Type: application/json
{
  "name": "Hurl application key",
  "kind": "gateway",
  "scopes": ["gateway:invoke", "gateway:usage:read"],
  "expires_at": null
}

HTTP 201
[Captures]
created_key_id: jsonpath "$.id"
created_key_secret: jsonpath "$.secret"
[Asserts]
jsonpath "$.account_id" == "21000000-0000-4000-8000-000000000001"
jsonpath "$.name" == "Hurl application key"
jsonpath "$.kind" == "gateway"
jsonpath "$.status" == "active"
jsonpath "$.secret" startsWith "giz_"
jsonpath "$.secret_hash" not exists


# An exact retry resolves to the original database row. Because credential
# plaintext is intentionally never persisted, the replay returns metadata and
# does not manufacture or disclose a second secret.
POST {{pay_url}}/account/v1/accounts/21000000-0000-4000-8000-000000000001/api_keys
Authorization: Bearer {{active_user_one_key}}
Idempotency-Key: story-01-create-gateway-key
Content-Type: application/json
{
  "name": "Hurl application key",
  "kind": "gateway",
  "scopes": ["gateway:invoke", "gateway:usage:read"],
  "expires_at": null
}

HTTP 200
[Asserts]
jsonpath "$.id" == "{{created_key_id}}"
jsonpath "$.secret" not exists


# Reusing the command key with a different payload is a deterministic conflict.
POST {{pay_url}}/account/v1/accounts/21000000-0000-4000-8000-000000000001/api_keys
Authorization: Bearer {{active_user_one_key}}
Idempotency-Key: story-01-create-gateway-key
Content-Type: application/json
{
  "name": "Changed application key",
  "kind": "gateway",
  "scopes": ["gateway:invoke"],
  "expires_at": null
}

HTTP 409
[Asserts]
jsonpath "$.error.code" == "idempotency_conflict"


# Read-after-write proves the row was committed to PostgreSQL. The plaintext and
# internal hash must both be absent from the metadata projection.
GET {{pay_url}}/account/v1/accounts/21000000-0000-4000-8000-000000000001/api_keys
Authorization: Bearer {{active_user_one_key}}

HTTP 200
[Asserts]
jsonpath "$.data" count == 2
jsonpath "$.data[1].id" == "{{created_key_id}}"
jsonpath "$.data[1].secret" not exists
jsonpath "$.data[1].secret_hash" not exists


# Gateway authentication with the captured secret proves creation stored usable
# database state rather than returning a fabricated response.
GET {{way_url}}/v1/models
Authorization: Bearer {{created_key_secret}}

HTTP 200
[Asserts]
jsonpath "$.object" == "list"


# Credential classes are not interchangeable: a Gateway key cannot access the
# first-party Account surface even though the signature and hash are valid.
GET {{pay_url}}/account/v1/me
Authorization: Bearer {{created_key_secret}}

HTTP 401
[Asserts]
jsonpath "$.error.code" == "unauthorized"


DELETE {{pay_url}}/account/v1/accounts/21000000-0000-4000-8000-000000000001/api_keys/{{created_key_id}}
Authorization: Bearer {{active_user_one_key}}
Idempotency-Key: story-01-revoke-gateway-key

HTTP 204


# DELETE is a command too: replaying it returns the persisted 204 instead of
# executing the revoke handler and appending another audit event.
DELETE {{pay_url}}/account/v1/accounts/21000000-0000-4000-8000-000000000001/api_keys/{{created_key_id}}
Authorization: Bearer {{active_user_one_key}}
Idempotency-Key: story-01-revoke-gateway-key

HTTP 204


# Gateway may continue using the last returned Quota until its short recheck
# interval expires. Advancing the regional test clock proves revocation is
# enforced on the next Quota query, without requiring GizWay to own customer
# identity or API-key state.
POST {{way_url}}/test/v1/clock/advance
Content-Type: application/json
{
  "by": "5m"
}

HTTP 200


GET {{way_url}}/v1/models
Authorization: Bearer {{created_key_secret}}

HTTP 401
[Asserts]
jsonpath "$.error.code" == "invalid_api_key"


# Another user cannot inspect the first user's key collection.
GET {{pay_url}}/account/v1/accounts/21000000-0000-4000-8000-000000000001/api_keys
Authorization: Bearer {{active_user_two_key}}

HTTP 404
[Asserts]
jsonpath "$.error.code" == "not_found"
