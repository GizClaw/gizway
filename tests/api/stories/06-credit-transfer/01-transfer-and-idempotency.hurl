# Story 06 — Transfer Credit atomically between two Gizway accounts.
#
# Product behavior:
# - The sender and recipient observe one final transfer with opposite effects.
# - The debit and credit are one balanced immutable ledger transaction.
# - Repeating the same command key and payload returns the original transfer
#   without another balance movement.
# - Reusing the key for a different payload is a conflict.
#
# Fixture: story-base
# Isolation: this file receives a new PostgreSQL schema and server process.
# covers: account.yaml#createCreditTransfer account.yaml#listCreditTransfers account.yaml#getAccountBalance

GET {{pay_url}}/account/v1/accounts/21000000-0000-4000-8000-000000000001/balance
Authorization: Bearer {{active_user_one_key}}

HTTP 200
[Asserts]
jsonpath "$.microcredits" == 100000000


POST {{pay_url}}/account/v1/accounts/21000000-0000-4000-8000-000000000001/transfers
Authorization: Bearer {{active_user_one_key}}
Idempotency-Key: story-06-transfer-one
Content-Type: application/json
{
  "recipient_account_id": "21000000-0000-4000-8000-000000000002",
  "amount": {"asset": "GIZ_CREDIT", "microcredits": 25000000},
  "note": "Story transfer"
}

HTTP 201
[Captures]
transfer_id: jsonpath "$.id"
[Asserts]
jsonpath "$.sender_account_id" == "21000000-0000-4000-8000-000000000001"
jsonpath "$.recipient_account_id" == "21000000-0000-4000-8000-000000000002"
jsonpath "$.amount.microcredits" == 25000000
jsonpath "$.status" == "succeeded"


# Same key and byte-equivalent business payload returns the original resource.
POST {{pay_url}}/account/v1/accounts/21000000-0000-4000-8000-000000000001/transfers
Authorization: Bearer {{active_user_one_key}}
Idempotency-Key: story-06-transfer-one
Content-Type: application/json
{
  "recipient_account_id": "21000000-0000-4000-8000-000000000002",
  "amount": {"asset": "GIZ_CREDIT", "microcredits": 25000000},
  "note": "Story transfer"
}

HTTP 200
[Asserts]
jsonpath "$.id" == "{{transfer_id}}"


# A changed payload under the same command key must not mutate either balance.
POST {{pay_url}}/account/v1/accounts/21000000-0000-4000-8000-000000000001/transfers
Authorization: Bearer {{active_user_one_key}}
Idempotency-Key: story-06-transfer-one
Content-Type: application/json
{
  "recipient_account_id": "21000000-0000-4000-8000-000000000002",
  "amount": {"asset": "GIZ_CREDIT", "microcredits": 1},
  "note": "Changed payload"
}

HTTP 409
[Asserts]
jsonpath "$.error.code" == "idempotency_conflict"


GET {{pay_url}}/account/v1/accounts/21000000-0000-4000-8000-000000000001/balance
Authorization: Bearer {{active_user_one_key}}

HTTP 200
[Asserts]
jsonpath "$.microcredits" == 75000000


GET {{pay_url}}/account/v1/accounts/21000000-0000-4000-8000-000000000002/balance
Authorization: Bearer {{active_user_two_key}}

HTTP 200
[Asserts]
jsonpath "$.microcredits" == 75000000


GET {{pay_url}}/account/v1/accounts/21000000-0000-4000-8000-000000000001/transfers
Authorization: Bearer {{active_user_one_key}}

HTTP 200
[Asserts]
jsonpath "$.data" count == 1
jsonpath "$.data[0].id" == "{{transfer_id}}"
jsonpath "$.data[0].direction" == "outgoing"


GET {{pay_url}}/account/v1/accounts/21000000-0000-4000-8000-000000000002/transfers
Authorization: Bearer {{active_user_two_key}}

HTTP 200
[Asserts]
jsonpath "$.data" count == 1
jsonpath "$.data[0].id" == "{{transfer_id}}"
jsonpath "$.data[0].direction" == "incoming"


# The list contract is bounded even when the current fixture has one row.
# Invalid cursors are rejected instead of being ignored and triggering a full
# table scan; valid limit=1 still returns the one transfer without continuation.
GET {{pay_url}}/account/v1/accounts/21000000-0000-4000-8000-000000000001/transfers?limit=1
Authorization: Bearer {{active_user_one_key}}

HTTP 200
[Asserts]
jsonpath "$.data" count == 1
jsonpath "$.page.has_more" == false


GET {{pay_url}}/account/v1/accounts/21000000-0000-4000-8000-000000000001/transfers?cursor=not-a-cursor
Authorization: Bearer {{active_user_one_key}}

HTTP 400
[Asserts]
jsonpath "$.error.code" == "invalid_request"
