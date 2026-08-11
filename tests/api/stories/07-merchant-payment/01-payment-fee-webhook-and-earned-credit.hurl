# Story 07 — Merchant Payment Intent, payer confirmation, fee and webhook.
#
# Product behavior:
# - Payment credentials are a separate class and work only on /pay/v1.
# - The approved merchant chooses the gross amount but never the fee. Gizway
#   snapshots the published 250 bps fee and rounds up: for 100,001
#   microcredits, fee=2,501 and merchant net=97,500 exactly.
# - Confirmation is one PostgreSQL transaction containing payer debit, merchant
#   net, platform revenue, transaction history and one durable webhook event.
# - The webhook is an actual signed HTTP request to the external fixture. Its
#   signing secret is returned once and omitted from endpoint listings.
# - Exact confirmation replay neither moves Credit nor emits another event.
# - Merchant-earned net Credit can invoke AI, but has no top-up lot and cannot
#   be sent to an original-route refund endpoint.
#
# Fixture: user two owns a pre-approved merchant account; user one is payer.
# Isolation: fresh PostgreSQL and external fake webhook receiver per Hurl file.
# covers: createApiKey createPaymentIntent getPaymentIntent getCheckoutPaymentIntent
# covers: confirmPaymentIntent cancelPaymentIntent reverseMerchantPayment listMerchantTransactions
# covers: createWebhookEndpoint listWebhookEndpoints getAccountBalance
# covers: updateWebhookEndpoint deleteWebhookEndpoint rotateWebhookEndpointSecret

POST {{base_url}}/account/v1/accounts/22000000-0000-4000-8000-000000000002/api_keys
Authorization: Bearer {{active_user_two_key}}
Idempotency-Key: story-07-payment-key
Content-Type: application/json
{
  "name": "Approved merchant payment key",
  "kind": "payment",
  "scopes": ["pay:intents:write", "pay:transactions:read", "pay:webhooks:write"]
}

HTTP 201
[Captures]
payment_key: jsonpath "$.secret"
payment_key_id: jsonpath "$.id"


POST {{base_url}}/account/v1/accounts/22000000-0000-4000-8000-000000000002/api_keys
Authorization: Bearer {{active_user_two_key}}
Idempotency-Key: story-07-gateway-key
Content-Type: application/json
{
  "name": "Merchant earned-Credit gateway key",
  "kind": "gateway",
  "scopes": ["gateway:invoke", "gateway:usage:read"]
}

HTTP 201
[Captures]
merchant_gateway_key: jsonpath "$.secret"


POST {{base_url}}/pay/v1/webhook_endpoints
Authorization: Bearer {{payment_key}}
Idempotency-Key: story-07-create-success-webhook
Content-Type: application/json
{
  "url": "{{payment_url}}/merchant-webhook",
  "events": ["payment_intent.succeeded", "transaction.reversed"]
}

HTTP 201
[Captures]
webhook_endpoint_id: jsonpath "$.id"
webhook_secret: jsonpath "$.signing_secret"
[Asserts]
jsonpath "$.status" == "active"
jsonpath "$.signing_secret" startsWith "whsec_"


# Exact create replay returns the same endpoint but never discloses its secret
# a second time. A different payload under this key would be HTTP 409.
POST {{base_url}}/pay/v1/webhook_endpoints
Authorization: Bearer {{payment_key}}
Idempotency-Key: story-07-create-success-webhook
Content-Type: application/json
{
  "url": "{{payment_url}}/merchant-webhook",
  "events": ["payment_intent.succeeded", "transaction.reversed"]
}

HTTP 200
[Asserts]
jsonpath "$.id" == "{{webhook_endpoint_id}}"
jsonpath "$.signing_secret" not exists


# Rotation returns only the new secret. Existing delivery rows retain their
# encrypted secret snapshot, while subsequent deliveries use this version.
POST {{base_url}}/pay/v1/webhook_endpoints/{{webhook_endpoint_id}}/rotate_secret
Authorization: Bearer {{payment_key}}
Idempotency-Key: story-07-rotate-webhook-secret

HTTP 200
[Captures]
rotated_webhook_secret: jsonpath "$.signing_secret"
[Asserts]
jsonpath "$.signing_secret" startsWith "whsec_"
jsonpath "$.signing_secret" != "{{webhook_secret}}"


# Secret rotation is an idempotent command. Repeating the same key returns the
# same generated value and does not rotate or audit twice.
POST {{base_url}}/pay/v1/webhook_endpoints/{{webhook_endpoint_id}}/rotate_secret
Authorization: Bearer {{payment_key}}
Idempotency-Key: story-07-rotate-webhook-secret

HTTP 200
[Asserts]
jsonpath "$.signing_secret" == "{{rotated_webhook_secret}}"


# Event filters are enforced by Gizway before an outbox delivery is created.
# This endpoint remains silent for the succeeded event below.
POST {{base_url}}/pay/v1/webhook_endpoints
Authorization: Bearer {{payment_key}}
Idempotency-Key: story-07-create-cancellation-webhook
Content-Type: application/json
{
  "url": "{{payment_url}}/merchant-webhook",
  "events": ["payment_intent.cancelled"]
}

HTTP 201
[Captures]
cancellation_endpoint_id: jsonpath "$.id"


# Disabled endpoints remain visible for operators but are excluded from new
# event fan-out.
PATCH {{base_url}}/pay/v1/webhook_endpoints/{{cancellation_endpoint_id}}
Authorization: Bearer {{payment_key}}
Idempotency-Key: story-07-disable-cancellation-webhook
Content-Type: application/json
{
  "status": "disabled"
}

HTTP 200
[Asserts]
jsonpath "$.status" == "disabled"


GET {{base_url}}/pay/v1/webhook_endpoints
Authorization: Bearer {{payment_key}}

HTTP 200
[Asserts]
jsonpath "$.data" count == 3
jsonpath "$.data[2].id" == "{{cancellation_endpoint_id}}"
jsonpath "$.data[2].status" == "disabled"


# Delete hides merchant configuration but preserves the database row and any
# historical delivery references.
DELETE {{base_url}}/pay/v1/webhook_endpoints/{{cancellation_endpoint_id}}
Authorization: Bearer {{payment_key}}
Idempotency-Key: story-07-delete-cancellation-webhook

HTTP 204


GET {{base_url}}/pay/v1/webhook_endpoints
Authorization: Bearer {{payment_key}}

HTTP 200
[Asserts]
jsonpath "$.data" count == 2
jsonpath "$.data[0].signing_secret" not exists
jsonpath "$.data[1].signing_secret" not exists


POST {{base_url}}/pay/v1/payment_intents
Authorization: Bearer {{payment_key}}
Idempotency-Key: story-07-order-one
Content-Type: application/json
{
  "service_id": "23000000-0000-4000-8000-000000000001",
  "external_order_id": "VPN-ORDER-0001",
  "amount": {"asset": "GIZ_CREDIT", "microcredits": 100001},
  "description": "Reviewed digital service",
  "expires_at": "2027-08-10T00:00:00Z",
  "metadata": {"service": "vpn"}
}

HTTP 201
[Captures]
payment_intent_id: jsonpath "$.id"
[Asserts]
jsonpath "$.status" == "created"
jsonpath "$.platform_fee.microcredits" == 2501
jsonpath "$.net_amount.microcredits" == 97500
jsonpath "$.fee_bps" == 250
jsonpath "$.checkout_url" startsWith "{{checkout_url}}/checkout/"


# external_order_id is merchant-unique even when a caller changes the
# Idempotency-Key. The API exposes a stable conflict and never leaks PostgreSQL or
# PostgreSQL table/constraint details.
POST {{base_url}}/pay/v1/payment_intents
Authorization: Bearer {{payment_key}}
Idempotency-Key: story-07-order-one-duplicate-key
Content-Type: application/json
{
  "service_id": "23000000-0000-4000-8000-000000000001",
  "external_order_id": "VPN-ORDER-0001",
  "amount": {"asset": "GIZ_CREDIT", "microcredits": 100001},
  "description": "Duplicate order must be rejected safely",
  "expires_at": "2027-08-10T00:00:00Z",
  "metadata": {}
}

HTTP 409
[Asserts]
jsonpath "$.error.code" == "idempotency_conflict"
body not contains "payment_intents"
body not contains "UNIQUE"
body not contains "constraint"


GET {{base_url}}/pay/v1/payment_intents/{{payment_intent_id}}
Authorization: Bearer {{payment_key}}

HTTP 200
[Asserts]
jsonpath "$.external_order_id" == "VPN-ORDER-0001"


GET {{base_url}}/pay/v1/checkout/payment_intents/{{payment_intent_id}}
Authorization: Bearer {{active_user_one_key}}

HTTP 200
[Asserts]
jsonpath "$.merchant_account_id" == "22000000-0000-4000-8000-000000000002"
jsonpath "$.amount.microcredits" == 100001
jsonpath "$.platform_fee.microcredits" == 2501


POST {{base_url}}/pay/v1/payment_intents/{{payment_intent_id}}/confirm
Authorization: Bearer {{active_user_one_key}}
Idempotency-Key: story-07-confirm-one
X-Request-ID: story-07-confirm-request

HTTP 200
[Asserts]
jsonpath "$.status" == "succeeded"
jsonpath "$.payer_account_id" == "21000000-0000-4000-8000-000000000001"


# Replay must not duplicate the three-way ledger posting or webhook event.
POST {{base_url}}/pay/v1/payment_intents/{{payment_intent_id}}/confirm
Authorization: Bearer {{active_user_one_key}}
Idempotency-Key: story-07-confirm-one

HTTP 200
[Asserts]
jsonpath "$.status" == "succeeded"


# Payer debit, merchant net, platform fee, final intent state and this audit
# row are one database transaction. Exact confirmation replay above cannot add
# a second audit event.
GET {{base_url}}/admin/v1/audit_events
Authorization: Bearer {{admin_api_key}}

HTTP 200
[Asserts]
jsonpath "$.data" count == 9
jsonpath "$.data[8].actor_type" == "user"
jsonpath "$.data[8].action" == "payment_intent.confirmed"
jsonpath "$.data[8].resource_id" == "{{payment_intent_id}}"
jsonpath "$.data[8].request_id" == "story-07-confirm-request"


# Merchant API mutations are attributed to the non-secret Payment key ID that
# authenticated them, never to the account owner who did not make the request.
# The action filter is part of the bounded Admin query contract and avoids an
# order-sensitive assertion against unrelated API-key creation audit rows.
GET {{base_url}}/admin/v1/audit_events?action=webhook_endpoint.created&limit=1
Authorization: Bearer {{admin_api_key}}

HTTP 200
[Captures]
audit_next_cursor: jsonpath "$.page.next_cursor"
[Asserts]
jsonpath "$.data" count == 1
jsonpath "$.data[0].actor_type" == "api_key"
jsonpath "$.data[0].actor_id" == "{{payment_key_id}}"
jsonpath "$.page.has_more" == true


# Cursor 1 continues after the first bounded row; the filtered result does not
# reload or return the first endpoint again.
GET {{base_url}}/admin/v1/audit_events?action=webhook_endpoint.created&limit=1&cursor={{audit_next_cursor}}
Authorization: Bearer {{admin_api_key}}

HTTP 200
[Asserts]
jsonpath "$.data" count == 1
jsonpath "$.data[0].actor_type" == "api_key"
jsonpath "$.data[0].actor_id" == "{{payment_key_id}}"
jsonpath "$.page.has_more" == false


GET {{base_url}}/pay/v1/transactions
Authorization: Bearer {{payment_key}}

HTTP 200
[Asserts]
jsonpath "$.data" count == 1
jsonpath "$.data[0].external_order_id" == "VPN-ORDER-0001"
jsonpath "$.data[0].gross_amount.microcredits" == 100001
jsonpath "$.data[0].platform_fee.microcredits" == 2501
jsonpath "$.data[0].net_amount.microcredits" == 97500


GET {{base_url}}/account/v1/accounts/21000000-0000-4000-8000-000000000001/balance
Authorization: Bearer {{active_user_one_key}}

HTTP 200
[Asserts]
jsonpath "$.microcredits" == 99899999


GET {{base_url}}/account/v1/accounts/22000000-0000-4000-8000-000000000002/balance
Authorization: Bearer {{active_user_two_key}}

HTTP 200
[Asserts]
jsonpath "$.microcredits" == 97500


GET {{payment_url}}/events

HTTP 200
[Asserts]
jsonpath "$.webhooks" == 1
jsonpath "$.last_webhook.signature" startsWith "v1="
jsonpath "$.last_webhook.payload.type" == "payment_intent.succeeded"
jsonpath "$.last_webhook.payload.data.object.external_order_id" == "VPN-ORDER-0001"


POST {{base_url}}/v1/chat/completions
Authorization: Bearer {{merchant_gateway_key}}
Idempotency-Key: story-07-merchant-ai
Content-Type: application/json
{
  "model": "story-text",
  "messages": [{"role": "user", "content": "spend earned merchant Credit"}],
  "stream": false
}

HTTP 200


GET {{base_url}}/account/v1/accounts/22000000-0000-4000-8000-000000000002/balance
Authorization: Bearer {{active_user_two_key}}

HTTP 200
[Asserts]
jsonpath "$.microcredits" == 97464


# There is no top-up provenance behind earned Credit, hence no withdrawal path.
POST {{base_url}}/account/v1/accounts/22000000-0000-4000-8000-000000000002/topups/00000000-0000-4000-8000-000000000000/refunds
Authorization: Bearer {{active_user_two_key}}
Idempotency-Key: story-07-earned-refund-denied
Content-Type: application/json
{
  "amount": {"asset": "GIZ_CREDIT", "microcredits": 1}
}

HTTP 404


# A Gateway key is valid credentials, but the wrong class for GizPay.
GET {{base_url}}/pay/v1/transactions
Authorization: Bearer {{merchant_gateway_key}}

HTTP 401


# Cancellation works only before confirmation.
POST {{base_url}}/pay/v1/payment_intents
Authorization: Bearer {{payment_key}}
Idempotency-Key: story-07-order-cancel
Content-Type: application/json
{
  "service_id": "23000000-0000-4000-8000-000000000001",
  "external_order_id": "VPN-ORDER-CANCEL",
  "amount": {"asset": "GIZ_CREDIT", "microcredits": 1000},
  "description": "Cancelled service",
  "expires_at": "2027-08-10T00:00:00Z",
  "metadata": {}
}

HTTP 201
[Captures]
cancel_intent_id: jsonpath "$.id"


POST {{base_url}}/pay/v1/payment_intents/{{cancel_intent_id}}/cancel
Authorization: Bearer {{payment_key}}
Idempotency-Key: story-07-cancel

HTTP 200
[Asserts]
jsonpath "$.status" == "cancelled"


POST {{base_url}}/pay/v1/payment_intents/{{payment_intent_id}}/cancel
Authorization: Bearer {{payment_key}}
Idempotency-Key: story-07-cancel-completed

HTTP 409


# A completed payment is corrected only through its linked compensating
# workflow. This separate payment is left unspent so the merchant still owns
# the full net amount required for an atomic reversal.
POST {{base_url}}/pay/v1/payment_intents
Authorization: Bearer {{payment_key}}
Idempotency-Key: story-07-order-reversal
Content-Type: application/json
{
  "service_id": "23000000-0000-4000-8000-000000000001",
  "external_order_id": "VPN-ORDER-REVERSAL",
  "amount": {"asset": "GIZ_CREDIT", "microcredits": 10000},
  "description": "Service later reversed",
  "expires_at": "2027-08-10T00:00:00Z",
  "metadata": {}
}

HTTP 201
[Captures]
reversal_payment_intent_id: jsonpath "$.id"


POST {{base_url}}/pay/v1/payment_intents/{{reversal_payment_intent_id}}/confirm
Authorization: Bearer {{active_user_one_key}}
Idempotency-Key: story-07-confirm-reversal-payment

HTTP 200
[Asserts]
jsonpath "$.status" == "succeeded"


POST {{base_url}}/pay/v1/payment_intents/{{reversal_payment_intent_id}}/reversals
Authorization: Bearer {{payment_key}}
Idempotency-Key: story-07-reverse-payment
X-Request-ID: story-07-reversal-request
Content-Type: application/json
{
  "reason": "Customer service was not delivered"
}

HTTP 201
[Captures]
merchant_reversal_id: jsonpath "$.id"
[Asserts]
jsonpath "$.object" == "merchant_payment_reversal"
jsonpath "$.payment_intent_id" == "{{reversal_payment_intent_id}}"
jsonpath "$.gross_amount.microcredits" == 10000
jsonpath "$.platform_fee.microcredits" == 250
jsonpath "$.net_amount.microcredits" == 9750
jsonpath "$.status" == "succeeded"


# Exact replay returns the same immutable reversal and posts no second entry or
# webhook. A different key for the same payment is rejected below.
POST {{base_url}}/pay/v1/payment_intents/{{reversal_payment_intent_id}}/reversals
Authorization: Bearer {{payment_key}}
Idempotency-Key: story-07-reverse-payment
Content-Type: application/json
{
  "reason": "Customer service was not delivered"
}

HTTP 200
[Asserts]
jsonpath "$.id" == "{{merchant_reversal_id}}"


POST {{base_url}}/pay/v1/payment_intents/{{reversal_payment_intent_id}}/reversals
Authorization: Bearer {{payment_key}}
Idempotency-Key: story-07-reverse-payment-again
Content-Type: application/json
{
  "reason": "Cannot reverse a settled payment twice"
}

HTTP 409
[Asserts]
jsonpath "$.error.code" == "reversal_conflict"


# The compensating posting restores the payer's exact gross Credit and removes
# the merchant net, without creating top-up refund entitlement.
GET {{base_url}}/account/v1/accounts/21000000-0000-4000-8000-000000000001/balance
Authorization: Bearer {{active_user_one_key}}

HTTP 200
[Asserts]
jsonpath "$.microcredits" == 99899999


GET {{base_url}}/account/v1/accounts/22000000-0000-4000-8000-000000000002/balance
Authorization: Bearer {{active_user_two_key}}

HTTP 200
[Asserts]
jsonpath "$.microcredits" == 97464


GET {{base_url}}/pay/v1/transactions
Authorization: Bearer {{payment_key}}

HTTP 200
[Asserts]
jsonpath "$.data" count == 2
jsonpath "$.data[?(@.payment_intent_id == '{{reversal_payment_intent_id}}')].status" == "reversed"


GET {{payment_url}}/events

HTTP 200
[Asserts]
jsonpath "$.webhooks" == 3
jsonpath "$.last_webhook.payload.type" == "transaction.reversed"
jsonpath "$.last_webhook.payload.data.object.id" == "{{merchant_reversal_id}}"


GET {{base_url}}/admin/v1/audit_events
Authorization: Bearer {{admin_api_key}}

HTTP 200
[Asserts]
jsonpath "$.data[?(@.resource_id == '{{merchant_reversal_id}}')].action" == "payment_intent.reversed"
jsonpath "$.data[?(@.resource_id == '{{merchant_reversal_id}}')].request_id" == "story-07-reversal-request"


# The story clock is fixed and explicitly advanced, so expiry remains
# executable in any calendar year. Advancing past the checkout deadline drives
# the durable created -> expired transition immediately; no payer debit or
# merchant settlement can occur afterward.
POST {{base_url}}/pay/v1/payment_intents
Authorization: Bearer {{payment_key}}
Idempotency-Key: story-07-expiring-intent
Content-Type: application/json
{
  "service_id": "23000000-0000-4000-8000-000000000001",
  "external_order_id": "VPN-ORDER-EXPIRING",
  "amount": {"asset": "GIZ_CREDIT", "microcredits": 1000},
  "description": "Clock-controlled expiry",
  "expires_at": "2026-08-11T12:00:01Z",
  "metadata": {}
}

HTTP 201
[Captures]
expiring_payment_intent_id: jsonpath "$.id"
[Asserts]
jsonpath "$.status" == "created"


POST {{base_url}}/test/v1/clock/advance
Content-Type: application/json
{
  "by": "2s"
}

HTTP 200
[Captures]
expired_at: jsonpath "$.now"
[Asserts]
jsonpath "$.now" startsWith "2026-08-11T12:00:02."


GET {{base_url}}/pay/v1/payment_intents/{{expiring_payment_intent_id}}
Authorization: Bearer {{payment_key}}

HTTP 200
[Asserts]
jsonpath "$.status" == "expired"
jsonpath "$.completed_at" == "{{expired_at}}"


POST {{base_url}}/pay/v1/payment_intents/{{expiring_payment_intent_id}}/confirm
Authorization: Bearer {{active_user_one_key}}
Idempotency-Key: story-07-expired-confirm

HTTP 409
[Asserts]
jsonpath "$.error.code" == "payment_not_confirmable"
