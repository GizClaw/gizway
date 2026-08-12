# Story 16 — Delayed regional Usage may make the posted balance negative.
#
# The ledger accepts that finite debt. The response clamps usable Quota to zero
# and future checks remain denied. A later top-up first repays known debt; only
# the positive remainder is spendable or refundable.
# covers: internal-gizpay.yaml#exchangeQuota account.yaml#createTopup account.yaml#listTopups account.yaml#getAccountBalance

# The seeded account owns 100 Credit. This immutable story publication prices
# request units at 9 microcredits, so 12,000,000 units charge 108 Credit and
# leave a posted balance of -8 Credit.
POST {{pay_url}}/internal/v1/quota/exchanges
Content-Type: application/json
{
  "api_key": "{{gateway_api_key}}",
  "usage": [
    {
      "ucgid": "ucg_story_16_overdraft",
      "operation_id": "op_story_16_overdraft",
      "public_model": "story-text",
      "model_variant_id": "91000000-0000-4000-8000-000000000001",
      "rate_publication_id": "ratepub_story_global_1",
      "metrics": {"request": 12000000},
      "started_at": "2026-08-11T12:05:00Z",
      "completed_at": "2026-08-11T12:05:01Z"
    }
  ]
}

HTTP 200
[Asserts]
jsonpath "$.status" == "denied"
jsonpath "$.quota.microcredits" == 0


POST {{pay_url}}/internal/v1/quota/exchanges
Content-Type: application/json
{
  "api_key": "{{gateway_api_key}}"
}

HTTP 200
[Asserts]
jsonpath "$.status" == "denied"
jsonpath "$.quota.microcredits" == 0


GET {{pay_url}}/account/v1/accounts/21000000-0000-4000-8000-000000000001/balance
Authorization: Bearer {{active_user_one_key}}

HTTP 200
[Asserts]
jsonpath "$.posted_microcredits" == -8000000
jsonpath "$.ai_spendable_microcredits" == 0
jsonpath "$.refundable_microcredits" == 0


# Top up 100 Credit. The existing payment fixture settles synchronously in
# story mode. Eight Credit repays debt; only 92 Credit becomes usable/refundable.
POST {{pay_url}}/account/v1/accounts/21000000-0000-4000-8000-000000000001/topups
Authorization: Bearer {{active_user_one_key}}
Idempotency-Key: story-16-debt-topup
Content-Type: application/json
{
  "fiat_currency": "USD",
  "fiat_amount_minor": 9000
}

HTTP 201
[Captures]
topup_id: jsonpath "$.id"
provider_reference: jsonpath "$.provider_reference"
[Asserts]
jsonpath "$.credit_amount.microcredits" == 100000000


# The fake payment provider signs the callback; neither Hurl nor GizPay invents
# provider authentication material inside the customer request.
POST {{payment_url}}/v1/test/confirm
Content-Type: application/json
{
  "callback_url": "{{pay_url}}/callbacks/v1/payment_events",
  "event_id": "storypay-event-16-debt-topup",
  "provider_reference": "{{provider_reference}}",
  "currency": "USD",
  "amount_minor": 9000
}

HTTP 200


GET {{pay_url}}/account/v1/accounts/21000000-0000-4000-8000-000000000001/topups
Authorization: Bearer {{active_user_one_key}}

HTTP 200
[Asserts]
jsonpath "$.data[0].id" == "{{topup_id}}"
jsonpath "$.data[0].status" == "succeeded"


POST {{pay_url}}/internal/v1/quota/exchanges
Content-Type: application/json
{
  "api_key": "{{gateway_api_key}}"
}

HTTP 200
[Asserts]
jsonpath "$.status" == "allowed"
jsonpath "$.quota.microcredits" == 92000000


GET {{pay_url}}/account/v1/accounts/21000000-0000-4000-8000-000000000001/balance
Authorization: Bearer {{active_user_one_key}}

HTTP 200
[Asserts]
jsonpath "$.posted_microcredits" == 92000000
jsonpath "$.ai_spendable_microcredits" == 92000000
jsonpath "$.refundable_microcredits" == 92000000
