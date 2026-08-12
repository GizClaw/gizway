# Story 01 — Read account-scoped usage and immutable transaction history.
#
# Product behavior:
# - A newly isolated story has no AI usage records.
# - The seeded opening balance is backed by one posted ledger transaction,
#   visible as an incoming top-up of exactly 100 Credits.
# - Both queries enforce account ownership.
#
# Fixture: story-base
# Isolation: this file receives a new PostgreSQL schema and server process.
# covers: account.yaml#listGatewayUsage account.yaml#listAccountTransactions

GET {{pay_url}}/account/v1/accounts/21000000-0000-4000-8000-000000000001/usage?from=2026-08-10T00%3A00%3A00Z&to=2026-08-11T00%3A00%3A00Z
Authorization: Bearer {{active_user_one_key}}

HTTP 200
[Asserts]
jsonpath "$.data" count == 0
jsonpath "$.page.has_more" == false


GET {{pay_url}}/account/v1/accounts/21000000-0000-4000-8000-000000000001/transactions
Authorization: Bearer {{active_user_one_key}}

HTTP 200
[Asserts]
jsonpath "$.data" count == 1
jsonpath "$.data[0].type" == "topup"
jsonpath "$.data[0].direction" == "incoming"
jsonpath "$.data[0].amount.asset" == "GIZ_CREDIT"
jsonpath "$.data[0].amount.microcredits" == 100000000
jsonpath "$.data[0].status" == "posted"


# Transaction history is also a bounded page. The opening transaction fits in
# limit=1 and therefore has no continuation; an invalid cursor is a client
# error, never permission to fall back to an unbounded query.
GET {{pay_url}}/account/v1/accounts/21000000-0000-4000-8000-000000000001/transactions?limit=1
Authorization: Bearer {{active_user_one_key}}

HTTP 200
[Asserts]
jsonpath "$.data" count == 1
jsonpath "$.page.has_more" == false


GET {{pay_url}}/account/v1/accounts/21000000-0000-4000-8000-000000000001/transactions?cursor=bad
Authorization: Bearer {{active_user_one_key}}

HTTP 400
[Asserts]
jsonpath "$.error.code" == "invalid_request"


# Another real account ID remains undiscoverable through either projection.
GET {{pay_url}}/account/v1/accounts/21000000-0000-4000-8000-000000000002/transactions
Authorization: Bearer {{active_user_one_key}}

HTTP 404
[Asserts]
jsonpath "$.error.code" == "not_found"
