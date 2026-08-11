# Story 01 — Reject missing, malformed, invalid, and suspended credentials.
#
# Product behavior:
# - Protected Account operations never execute without a valid credential.
# - A cryptographically valid fixture credential still fails when its owning
#   user is suspended; lifecycle state is part of authentication.
# - Authentication errors do not disclose whether a key, user, or account was
#   the rejected object.
#
# Fixture: story-base
# Isolation: this file receives a new PostgreSQL schema and server process.
# covers: getCurrentUser

# Missing Authorization header.
GET {{base_url}}/account/v1/me

HTTP 401
[Asserts]
jsonpath "$.error.code" == "unauthorized"


# An unsupported authentication scheme is malformed even if it carries a real
# fixture secret.
GET {{base_url}}/account/v1/me
Authorization: Basic {{active_user_one_key}}

HTTP 401
[Asserts]
jsonpath "$.error.code" == "unauthorized"


# Unknown secrets cannot authenticate.
GET {{base_url}}/account/v1/me
Authorization: Bearer definitely-not-a-real-key

HTTP 401
[Asserts]
jsonpath "$.error.code" == "unauthorized"


# The key hash exists and the key row is active, but its owning user is
# suspended. Authentication must reject it before reaching the handler.
GET {{base_url}}/account/v1/me
Authorization: Bearer {{suspended_user_key}}

HTTP 401
[Asserts]
jsonpath "$.error.code" == "unauthorized"
