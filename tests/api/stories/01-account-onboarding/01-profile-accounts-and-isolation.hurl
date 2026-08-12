# Story 01 — Inspect the authenticated profile, personal account, and balance.
#
# Product behavior:
# - An active fixture principal can read only its own profile and accounts.
# - A personal account exposes its posted GIZ_CREDIT balance through the API.
# - Guessing another user's account ID does not reveal its balance.
#
# Fixture: story-base
# Isolation: this file receives a new PostgreSQL schema and server process.
# covers: account.yaml#getCurrentUser account.yaml#listAccounts account.yaml#getAccountBalance

# The profile is resolved from the authenticated database principal. It is not
# a response synthesized from the bearer value or an in-memory fixture.
GET {{pay_url}}/account/v1/me
Authorization: Bearer {{active_user_one_key}}

HTTP 200
[Asserts]
jsonpath "$.id" == "11000000-0000-4000-8000-000000000001"
jsonpath "$.email" == "active-one@gizway.test"
jsonpath "$.display_name" == "Active User One"
jsonpath "$.status" == "active"


# Exactly one personal account is part of the immutable story-base fixture.
# Mutable merchant accounts must be created through their owning story APIs.
GET {{pay_url}}/account/v1/accounts
Authorization: Bearer {{active_user_one_key}}

HTTP 200
[Asserts]
jsonpath "$.data" count == 1
jsonpath "$.data[0].id" == "21000000-0000-4000-8000-000000000001"
jsonpath "$.data[0].kind" == "personal"
jsonpath "$.data[0].status" == "active"


# The balance is calculated from posted ledger entries. The opening fixture
# transaction credits 100 Credits, represented as integer microcredits.
GET {{pay_url}}/account/v1/accounts/21000000-0000-4000-8000-000000000001/balance
Authorization: Bearer {{active_user_one_key}}

HTTP 200
[Asserts]
jsonpath "$.account_id" == "21000000-0000-4000-8000-000000000001"
jsonpath "$.asset" == "GIZ_CREDIT"
jsonpath "$.microcredits" == 100000000


# Tenant isolation deliberately uses a real account ID owned by another user.
# A not-found response avoids confirming whether that account exists.
GET {{pay_url}}/account/v1/accounts/21000000-0000-4000-8000-000000000002/balance
Authorization: Bearer {{active_user_one_key}}

HTTP 404
[Asserts]
jsonpath "$.error.code" == "not_found"
