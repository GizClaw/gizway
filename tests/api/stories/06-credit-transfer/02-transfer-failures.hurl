# Story 06 — Reject invalid, unauthorized, and unaffordable transfers.
#
# Every failure is checked through balances afterward, proving validation did
# not leave a partial debit, credit, or pending transfer in PostgreSQL.
#
# Fixture: story-base
# Isolation: this file receives a new PostgreSQL schema and server process.
# covers: account.yaml#createCreditTransfer account.yaml#getAccountBalance

# An account cannot transfer to itself.
POST {{pay_url}}/account/v1/accounts/21000000-0000-4000-8000-000000000001/transfers
Authorization: Bearer {{active_user_one_key}}
Idempotency-Key: story-06-self-transfer
Content-Type: application/json
{
  "recipient_account_id": "21000000-0000-4000-8000-000000000001",
  "amount": {"asset": "GIZ_CREDIT", "microcredits": 1}
}

HTTP 400
[Asserts]
jsonpath "$.error.code" == "invalid_request"


# More than the posted balance is rejected atomically.
POST {{pay_url}}/account/v1/accounts/21000000-0000-4000-8000-000000000001/transfers
Authorization: Bearer {{active_user_one_key}}
Idempotency-Key: story-06-insufficient
Content-Type: application/json
{
  "recipient_account_id": "21000000-0000-4000-8000-000000000002",
  "amount": {"asset": "GIZ_CREDIT", "microcredits": 100000001}
}

HTTP 409
[Asserts]
jsonpath "$.error.code" == "insufficient_balance"


# A user cannot spend an account owned by somebody else.
POST {{pay_url}}/account/v1/accounts/21000000-0000-4000-8000-000000000002/transfers
Authorization: Bearer {{active_user_one_key}}
Idempotency-Key: story-06-cross-account
Content-Type: application/json
{
  "recipient_account_id": "21000000-0000-4000-8000-000000000001",
  "amount": {"asset": "GIZ_CREDIT", "microcredits": 1}
}

HTTP 404
[Asserts]
jsonpath "$.error.code" == "not_found"


GET {{pay_url}}/account/v1/accounts/21000000-0000-4000-8000-000000000001/balance
Authorization: Bearer {{active_user_one_key}}

HTTP 200
[Asserts]
jsonpath "$.microcredits" == 100000000
