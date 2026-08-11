# Story 01 — Real User session issuance, rotation and logout.
#
# This file is the complete browser-session contract:
# - Email/password authentication uses bcrypt data in the real SQL database.
# - The returned secret is a bearer header credential, never a cookie; the
#   response therefore declares that cookie-style CSRF protection is not used.
# - Login idempotency is scoped by normalized email, so two users may safely
#   choose the same Idempotency-Key without sharing a command namespace.
# - Refresh atomically revokes the predecessor and issues one replacement.
#   Exact refresh replay returns the original replacement token.
# - Logout revokes immediately; neither old nor logged-out token authenticates.
#
# Isolation: a fresh PostgreSQL database is created for this Hurl file.
# covers: loginUser refreshUserSession logoutUser getCurrentUser

POST {{base_url}}/account/v1/auth/login
Idempotency-Key: story-01-shared-login-key
Content-Type: application/json
{
  "email": "active-one@gizway.test",
  "password": "story-user-password"
}

HTTP 200
[Captures]
user_session_one: jsonpath "$.access_token"
[Asserts]
jsonpath "$.user.id" == "11000000-0000-4000-8000-000000000001"
jsonpath "$.token_type" == "Bearer"
jsonpath "$.csrf_protection" == "not_required_bearer_header"


# Exact login replay returns the encrypted-at-rest command response and cannot
# create a second database session.
POST {{base_url}}/account/v1/auth/login
Idempotency-Key: story-01-shared-login-key
Content-Type: application/json
{
  "email": "active-one@gizway.test",
  "password": "story-user-password"
}

HTTP 200
[Asserts]
jsonpath "$.access_token" == "{{user_session_one}}"


# The same transport key belongs to a separate normalized login scope for a
# different identity; it must not collide with user one's command.
POST {{base_url}}/account/v1/auth/login
Idempotency-Key: story-01-shared-login-key
Content-Type: application/json
{
  "email": "active-two@gizway.test",
  "password": "story-user-password"
}

HTTP 200
[Captures]
user_session_two: jsonpath "$.access_token"
[Asserts]
jsonpath "$.user.id" == "11000000-0000-4000-8000-000000000002"
jsonpath "$.access_token" != "{{user_session_one}}"


# A correct password cannot bypass lifecycle status.
POST {{base_url}}/account/v1/auth/login
Idempotency-Key: story-01-suspended-login
Content-Type: application/json
{
  "email": "suspended@gizway.test",
  "password": "story-user-password"
}

HTTP 401


POST {{base_url}}/account/v1/auth/refresh
Authorization: Bearer {{user_session_one}}
Idempotency-Key: story-01-refresh

HTTP 200
[Captures]
refreshed_user_session: jsonpath "$.access_token"
[Asserts]
jsonpath "$.user.id" == "11000000-0000-4000-8000-000000000001"
jsonpath "$.access_token" != "{{user_session_one}}"


# The old bearer was revoked in the same transaction as replacement issuance.
GET {{base_url}}/account/v1/me
Authorization: Bearer {{user_session_one}}

HTTP 401


# The generic command journal resolves exact refresh replay before invoking
# authentication again, so the caller recovers the same one-time result even
# though the predecessor token is now revoked.
POST {{base_url}}/account/v1/auth/refresh
Authorization: Bearer {{user_session_one}}
Idempotency-Key: story-01-refresh

HTTP 200
[Asserts]
jsonpath "$.access_token" == "{{refreshed_user_session}}"


GET {{base_url}}/account/v1/me
Authorization: Bearer {{refreshed_user_session}}

HTTP 200
[Asserts]
jsonpath "$.email" == "active-one@gizway.test"


POST {{base_url}}/account/v1/auth/logout
Authorization: Bearer {{refreshed_user_session}}
Idempotency-Key: story-01-logout

HTTP 204


GET {{base_url}}/account/v1/me
Authorization: Bearer {{refreshed_user_session}}

HTTP 401


# Clean up the independently scoped second user session as a real revocation.
POST {{base_url}}/account/v1/auth/logout
Authorization: Bearer {{user_session_two}}
Idempotency-Key: story-01-logout-user-two

HTTP 204
