# Story 05 — Signed fiat top-up, purchased-Credit consumption, and refund.
#
# Product behavior:
# - StoryPay is a separate HTTP process. Gizway creates a real provider checkout
#   and accepts only HMAC-SHA256 authenticated callbacks.
# - The provider event ID is durable deduplication state: delivering the same
#   success event twice creates one invoice, one Credit lot and one balanced
#   ledger posting.
# - The top-up snapshots baseline 100 cents/Credit and discounted
#   90 cents/Credit (1000 bps). 900 cents therefore issues 10,000,000
#   microcredits using integer arithmetic.
# - Outgoing AI usage consumes refundable purchased provenance first. Only the
#   remaining lot can be returned, and refund fiat is rounded down at the
#   original effective rate so the provider never pays excess value.
# - The refund adapter sends the refund to the stored original provider
#   reference; this API cannot name an arbitrary destination.
#
# Fixture: a zero-balance account is created through the public Account API.
# Isolation: fresh PostgreSQL and separate fake AI/payment processes per file.
# covers: createMerchantAccount createApiKey createTopup listTopups
# covers: refundUnusedTopup getAccountBalance listAccountTransactions listInvoices getInvoice

POST {{base_url}}/account/v1/merchant_accounts
Authorization: Bearer {{active_user_one_key}}
Idempotency-Key: story-05-zero-account
Content-Type: application/json
{
  "name": "Refund provenance account",
  "legal_name": "Refund Provenance LLC",
  "public_name": "Refund Provenance",
  "country_code": "US"
}

HTTP 201
[Captures]
topup_account_id: jsonpath "$.account.id"


POST {{base_url}}/account/v1/accounts/{{topup_account_id}}/api_keys
Authorization: Bearer {{active_user_one_key}}
Idempotency-Key: story-05-gateway-key
Content-Type: application/json
{
  "name": "Top-up consumption key",
  "kind": "gateway",
  "scopes": ["gateway:invoke", "gateway:usage:read"]
}

HTTP 201
[Captures]
topup_gateway_key: jsonpath "$.secret"


# Force the fake provider to commit its checkout effect and then return an
# ambiguous 500 response. Gizway cannot know whether the effect happened, so a
# retry must use the same provider Idempotency-Key and converge safely.
POST {{payment_url}}/v1/test/fail_next_checkout_after_commit

HTTP 204


POST {{base_url}}/account/v1/accounts/{{topup_account_id}}/topups
Authorization: Bearer {{active_user_one_key}}
Idempotency-Key: story-05-topup
Content-Type: application/json
{
  "fiat_currency": "USD",
  "fiat_amount_minor": 900
}

HTTP 503
[Asserts]
jsonpath "$.error.code" == "provider_unavailable"


POST {{base_url}}/account/v1/accounts/{{topup_account_id}}/topups
Authorization: Bearer {{active_user_one_key}}
Idempotency-Key: story-05-topup
Content-Type: application/json
{
  "fiat_currency": "USD",
  "fiat_amount_minor": 900
}

HTTP 201
[Captures]
topup_id: jsonpath "$.id"
provider_reference: jsonpath "$.provider_reference"
[Asserts]
jsonpath "$.status" == "pending"
jsonpath "$.rate.base.fiat_minor" == 100
jsonpath "$.rate.effective.fiat_minor" == 90
jsonpath "$.rate.discount_bps" == 1000
jsonpath "$.credit_amount.microcredits" == 10000000
jsonpath "$.refundable_amount.microcredits" == 0


# Retrying the same customer command may repeat the network request after an
# ambiguous response, but the stable top-up ID is also sent as the provider's
# Idempotency-Key. The provider therefore creates exactly one checkout.
POST {{base_url}}/account/v1/accounts/{{topup_account_id}}/topups
Authorization: Bearer {{active_user_one_key}}
Idempotency-Key: story-05-topup
Content-Type: application/json
{
  "fiat_currency": "USD",
  "fiat_amount_minor": 900
}

HTTP 200
[Asserts]
jsonpath "$.id" == "{{topup_id}}"
jsonpath "$.provider_reference" == "{{provider_reference}}"


# The external fixture signs the exact callback body before sending it to
# Gizway. Hurl never knows or fabricates the callback signature.
# A correctly signed event outside the five-minute replay window is still
# rejected before event identity or balance mutation, in both time directions.
POST {{payment_url}}/v1/test/confirm
Content-Type: application/json
{
  "callback_url": "{{base_url}}/callbacks/v1/payment_events",
  "event_id": "storypay-event-05-expired",
  "provider_reference": "{{provider_reference}}",
  "currency": "USD",
  "amount_minor": 900,
  "timestamp_offset_seconds": -3600
}

HTTP 401


POST {{payment_url}}/v1/test/confirm
Content-Type: application/json
{
  "callback_url": "{{base_url}}/callbacks/v1/payment_events",
  "event_id": "storypay-event-05-future",
  "provider_reference": "{{provider_reference}}",
  "currency": "USD",
  "amount_minor": 900,
  "timestamp_offset_seconds": 3600
}

HTTP 401


POST {{payment_url}}/v1/test/confirm
Content-Type: application/json
{
  "callback_url": "{{base_url}}/callbacks/v1/payment_events",
  "event_id": "storypay-event-05-one",
  "provider_reference": "{{provider_reference}}",
  "currency": "USD",
  "amount_minor": 900
}

HTTP 200


# Exact external redelivery is acknowledged without duplicate issuance.
POST {{payment_url}}/v1/test/confirm
Content-Type: application/json
{
  "callback_url": "{{base_url}}/callbacks/v1/payment_events",
  "event_id": "storypay-event-05-one",
  "provider_reference": "{{provider_reference}}",
  "currency": "USD",
  "amount_minor": 900
}

HTTP 200


# One successful event creates one immutable invoice even after the exact
# provider redelivery above. Provider routing and credential fields are not
# part of the customer receipt.
GET {{base_url}}/account/v1/accounts/{{topup_account_id}}/invoices
Authorization: Bearer {{active_user_one_key}}

HTTP 200
[Captures]
invoice_id: jsonpath "$.data[0].id"
[Asserts]
jsonpath "$.data" count == 1
jsonpath "$.data[0].topup_id" == "{{topup_id}}"
jsonpath "$.data[0].invoice_number" == "INV-storypay-event-05-one"
jsonpath "$.data[0].fiat_currency" == "USD"
jsonpath "$.data[0].fiat_amount_minor" == 900
jsonpath "$.data[0].provider_reference" not exists
jsonpath "$.data[0].secret" not exists


GET {{base_url}}/account/v1/accounts/{{topup_account_id}}/invoices/{{invoice_id}}
Authorization: Bearer {{active_user_one_key}}

HTTP 200
[Asserts]
jsonpath "$.id" == "{{invoice_id}}"
jsonpath "$.topup_id" == "{{topup_id}}"


# Ownership is derived from the authenticated user, not only the account path.
GET {{base_url}}/account/v1/accounts/{{topup_account_id}}/invoices/{{invoice_id}}
Authorization: Bearer {{active_user_two_key}}

HTTP 404


GET {{base_url}}/account/v1/accounts/{{topup_account_id}}/balance
Authorization: Bearer {{active_user_one_key}}

HTTP 200
[Asserts]
jsonpath "$.microcredits" == 10000000


POST {{base_url}}/v1/chat/completions
Authorization: Bearer {{topup_gateway_key}}
Idempotency-Key: story-05-consume-credit
Content-Type: application/json
{
  "model": "story-text",
  "messages": [{"role": "user", "content": "consume purchased Credit"}],
  "stream": false
}

HTTP 200


GET {{base_url}}/account/v1/accounts/{{topup_account_id}}/topups
Authorization: Bearer {{active_user_one_key}}

HTTP 200
[Asserts]
jsonpath "$.data" count == 1
jsonpath "$.data[0].status" == "succeeded"
jsonpath "$.data[0].refundable_amount.microcredits" == 9999964


# Top-up history is a bounded cursor page. A malformed cursor must be rejected
# rather than ignored; otherwise a growing financial history becomes an
# unbounded production query even though OpenAPI advertises pagination.
GET {{base_url}}/account/v1/accounts/{{topup_account_id}}/topups?limit=1
Authorization: Bearer {{active_user_one_key}}

HTTP 200
[Asserts]
jsonpath "$.data" count == 1
jsonpath "$.page.has_more" == false


GET {{base_url}}/account/v1/accounts/{{topup_account_id}}/topups?cursor=bad
Authorization: Bearer {{active_user_one_key}}

HTTP 400
[Asserts]
jsonpath "$.error.code" == "invalid_request"


# Refund has the same ambiguous external-commit window. The first request
# creates one durable pending refund and the provider commits one refund effect
# before returning 500; replay finishes that exact refund, not a second one.
POST {{payment_url}}/v1/test/fail_next_refund_after_commit

HTTP 204


POST {{base_url}}/account/v1/accounts/{{topup_account_id}}/topups/{{topup_id}}/refunds
Authorization: Bearer {{active_user_one_key}}
Idempotency-Key: story-05-refund
Content-Type: application/json
{
  "amount": {"asset": "GIZ_CREDIT", "microcredits": 5000000}
}

HTTP 503
[Asserts]
jsonpath "$.error.code" == "provider_unavailable"


POST {{base_url}}/account/v1/accounts/{{topup_account_id}}/topups/{{topup_id}}/refunds
Authorization: Bearer {{active_user_one_key}}
Idempotency-Key: story-05-refund
Content-Type: application/json
{
  "amount": {"asset": "GIZ_CREDIT", "microcredits": 5000000}
}

HTTP 200
[Asserts]
jsonpath "$.status" == "succeeded"
jsonpath "$.credit_amount.microcredits" == 5000000
jsonpath "$.fiat_amount_minor" == 450


# The same command replays the original refund and does not call StoryPay again.
POST {{base_url}}/account/v1/accounts/{{topup_account_id}}/topups/{{topup_id}}/refunds
Authorization: Bearer {{active_user_one_key}}
Idempotency-Key: story-05-refund
Content-Type: application/json
{
  "amount": {"asset": "GIZ_CREDIT", "microcredits": 5000000}
}

HTTP 200
[Asserts]
jsonpath "$.status" == "succeeded"


# Spent and already-refunded Credit is no longer refundable.
POST {{base_url}}/account/v1/accounts/{{topup_account_id}}/topups/{{topup_id}}/refunds
Authorization: Bearer {{active_user_one_key}}
Idempotency-Key: story-05-over-refund
Content-Type: application/json
{
  "amount": {"asset": "GIZ_CREDIT", "microcredits": 5000000}
}

HTTP 409
[Asserts]
jsonpath "$.error.code" == "not_refundable"


GET {{base_url}}/account/v1/accounts/{{topup_account_id}}/balance
Authorization: Bearer {{active_user_one_key}}

HTTP 200
[Asserts]
jsonpath "$.microcredits" == 4999964


GET {{payment_url}}/events

HTTP 200
[Asserts]
jsonpath "$.callbacks" == 4
jsonpath "$.checkouts" == 1
jsonpath "$.refunds" == 1


GET {{base_url}}/account/v1/accounts/{{topup_account_id}}/transactions
Authorization: Bearer {{active_user_one_key}}

HTTP 200
[Asserts]
jsonpath "$.data" count == 3
jsonpath "$.data[0].type" == "refund"
jsonpath "$.data[1].type" == "gateway_usage"
jsonpath "$.data[2].type" == "topup"


# A definitive provider rejection is not an ambiguous retry. Gizway records a
# terminal failed refund and immediately releases the temporary Credit hold;
# replay returns that same failed resource without calling the provider again.
POST {{payment_url}}/v1/test/fail_next_refund

HTTP 204


POST {{base_url}}/account/v1/accounts/{{topup_account_id}}/topups/{{topup_id}}/refunds
Authorization: Bearer {{active_user_one_key}}
Idempotency-Key: story-05-definitive-refund-failure
Content-Type: application/json
{
  "amount": {"asset": "GIZ_CREDIT", "microcredits": 1000000}
}

HTTP 202
[Asserts]
jsonpath "$.status" == "failed"
jsonpath "$.completed_at" exists


GET {{base_url}}/account/v1/accounts/{{topup_account_id}}/balance
Authorization: Bearer {{active_user_one_key}}

HTTP 200
[Asserts]
jsonpath "$.microcredits" == 4999964


POST {{base_url}}/account/v1/accounts/{{topup_account_id}}/topups/{{topup_id}}/refunds
Authorization: Bearer {{active_user_one_key}}
Idempotency-Key: story-05-definitive-refund-failure
Content-Type: application/json
{
  "amount": {"asset": "GIZ_CREDIT", "microcredits": 1000000}
}

HTTP 200
[Asserts]
jsonpath "$.status" == "failed"


GET {{payment_url}}/events

HTTP 200
[Asserts]
jsonpath "$.refunds" == 2
