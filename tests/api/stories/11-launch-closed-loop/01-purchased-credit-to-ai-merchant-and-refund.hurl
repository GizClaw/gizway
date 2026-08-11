# Story 11 — Limited-launch business loop from catalog to eligible refund.
#
# This is deliberately a composed acceptance test, not a second implementation
# of the lower-level stories. Every step uses a public API and then proves the
# resulting cross-domain state through another public API.
#
# Complete flow:
# 1. Admin publishes a provider endpoint, canonical model, variant and prices.
# 2. A merchant applies and an administrator records an enhanced review approval.
# 3. Payer buys Credit; one signed provider event issues one refundable lot.
# 4. Payer spends purchased Credit on exact-metered AI.
# 5. Merchant registers separate Payment/Gateway keys and a signed webhook.
# 6. Payer confirms the disclosed order; gross=fee+net settles atomically.
# 7. Merchant spends earned net Credit on the same AI model.
# 8. Payer refunds only the purchased-lot remainder to the original provider.
#
# Expected integer results:
# - Top-up: 900 USD cents -> 10,000,000 microcredits at 90 cents/Credit.
# - Each fake AI call: ceil(10*1800/1000)+ceil(5*3600/1000)=36.
# - Order: gross 100,001; ceil(250 bps)=2,501; merchant net=97,500.
# - Payer final: 50,000,000 + 10,000,000 - 36 - 100,001 - 5,000,000
#   = 54,899,963. Merchant final: 97,500 - 36 = 97,464.
#
# Isolation: fresh story-base PostgreSQL and separate AI/payment/webhook fakes.
# protocol-case: launch.openai.chat purchased-and-earned-credit closed-loop
# covers: createProvider createProviderEndpoint createModel createModelVariant
# covers: publishModelVariantPrice createMerchantAccount decideMerchantReview
# covers: createTopup listTopups createApiKey createWebhookEndpoint
# covers: createMerchantService listMerchantServices decideMerchantService
# covers: createPaymentIntent getCheckoutPaymentIntent confirmPaymentIntent
# covers: listMerchantTransactions refundUnusedTopup getAccountBalance
# covers: listAdminPayments
# protocol covers: OpenAI non-streaming POST /v1/chat/completions

POST {{base_url}}/admin/v1/providers
Authorization: Bearer {{admin_api_key}}
Idempotency-Key: story-11-provider
Content-Type: application/json
{
  "slug": "story-launch-provider",
  "name": "Story Launch Provider"
}

HTTP 201
[Captures]
provider_id: jsonpath "$.id"


POST {{base_url}}/admin/v1/providers/{{provider_id}}/endpoints
Authorization: Bearer {{admin_api_key}}
Idempotency-Key: story-11-endpoint
Content-Type: application/json
{
  "name": "Launch fake endpoint",
  "base_url": "{{catalog_fake_url}}",
  "credential": "story-catalog-provider-key",
  "priority": 1,
  "weight": 100
}

HTTP 201
[Captures]
endpoint_id: jsonpath "$.id"


POST {{base_url}}/admin/v1/models
Authorization: Bearer {{admin_api_key}}
Idempotency-Key: story-11-model
Content-Type: application/json
{
  "slug": "story-launch",
  "name": "Story Launch Model",
  "modality": ["text"],
  "metadata": {"launch": true}
}

HTTP 201
[Captures]
model_id: jsonpath "$.id"


POST {{base_url}}/admin/v1/models/{{model_id}}/variants
Authorization: Bearer {{admin_api_key}}
Idempotency-Key: story-11-variant
Content-Type: application/json
{
  "provider_endpoint_id": "{{endpoint_id}}",
  "provider_model_name": "fake-text-launch",
  "variant_slug": "launch-fake",
  "capabilities": {"chat": true},
  "context_window": 8192,
  "max_output_tokens": 2048
}

HTTP 201
[Captures]
variant_id: jsonpath "$.id"


POST {{base_url}}/admin/v1/model_variants/{{variant_id}}/prices
Authorization: Bearer {{admin_api_key}}
Idempotency-Key: story-11-input-price
Content-Type: application/json
{
  "metric": "input_token", "unit_size": 1000,
  "upstream_cost_microcredits": 1000,
  "base_customer_price_microcredits": 2000,
  "customer_price_microcredits": 1800,
  "discount_bps": 1000,
  "valid_from": "2026-08-09T00:00:00Z", "valid_until": null
}

HTTP 201


POST {{base_url}}/admin/v1/model_variants/{{variant_id}}/prices
Authorization: Bearer {{admin_api_key}}
Idempotency-Key: story-11-output-price
Content-Type: application/json
{
  "metric": "output_token", "unit_size": 1000,
  "upstream_cost_microcredits": 2000,
  "base_customer_price_microcredits": 4000,
  "customer_price_microcredits": 3600,
  "discount_bps": 1000,
  "valid_from": "2026-08-09T00:00:00Z", "valid_until": null
}

HTTP 201


POST {{base_url}}/admin/v1/model_variants/{{variant_id}}/prices
Authorization: Bearer {{admin_api_key}}
Idempotency-Key: story-11-cached-input-price
Content-Type: application/json
{
  "metric": "cached_input_token", "unit_size": 1000,
  "upstream_cost_microcredits": 500,
  "base_customer_price_microcredits": 1000,
  "customer_price_microcredits": 900,
  "discount_bps": 1000,
  "valid_from": "2026-08-09T00:00:00Z", "valid_until": null
}

HTTP 201


POST {{base_url}}/account/v1/merchant_accounts
Authorization: Bearer {{active_user_one_key}}
Idempotency-Key: story-11-merchant
Content-Type: application/json
{
  "name": "Launch VPN Merchant",
  "legal_name": "Launch VPN Merchant LLC",
  "public_name": "Launch VPN",
  "country_code": "US",
  "website_url": "https://launch-vpn.gizway.test"
}

HTTP 201
[Captures]
merchant_account_id: jsonpath "$.account.id"


# Registration calls the external mandatory risk boundary before either the
# merchant or service can be approved. The response exposes reproducible
# KYC/KYB/sanctions outcomes and limits, never the provider reference.
POST {{base_url}}/account/v1/merchant_accounts/{{merchant_account_id}}/services
Authorization: Bearer {{active_user_one_key}}
Idempotency-Key: story-11-vpn-service
Content-Type: application/json
{
  "service_code": "vpn",
  "name": "Launch VPN Subscription",
  "description": "Reviewed limited-network VPN service",
  "interface_set": ["checkout", "webhook"]
}

HTTP 201
[Captures]
merchant_service_id: jsonpath "$.service.id"
[Asserts]
jsonpath "$.service.status" == "pending"
jsonpath "$.risk_decision.decision" == "allow"
jsonpath "$.risk_decision.kyc_status" == "verified"
jsonpath "$.risk_decision.kyb_status" == "verified"
jsonpath "$.risk_decision.sanctions_status" == "clear"
jsonpath "$.risk_decision.provider_reference" not exists
jsonpath "$.service.max_transaction_microcredits" == 10000000


# Provider idempotency and database idempotency compose: replay returns the
# same service and does not run a second assessment.
POST {{base_url}}/account/v1/merchant_accounts/{{merchant_account_id}}/services
Authorization: Bearer {{active_user_one_key}}
Idempotency-Key: story-11-vpn-service
Content-Type: application/json
{
  "service_code": "vpn",
  "name": "Launch VPN Subscription",
  "description": "Reviewed limited-network VPN service",
  "interface_set": ["checkout", "webhook"]
}

HTTP 200
[Asserts]
jsonpath "$.service.id" == "{{merchant_service_id}}"


# A deterministic sanctions match stays unapprovable even if an administrator
# attempts to override it.
POST {{base_url}}/account/v1/merchant_accounts/{{merchant_account_id}}/services
Authorization: Bearer {{active_user_one_key}}
Idempotency-Key: story-11-blocked-service
Content-Type: application/json
{
  "service_code": "blocked",
  "name": "Blocked Service",
  "description": "Must fail closed",
  "interface_set": ["checkout"]
}

HTTP 201
[Captures]
blocked_service_id: jsonpath "$.service.id"
[Asserts]
jsonpath "$.risk_decision.decision" == "deny"
jsonpath "$.risk_decision.sanctions_status" == "match"


POST {{base_url}}/admin/v1/merchants/{{merchant_account_id}}/decision
Authorization: Bearer {{admin_api_key}}
Idempotency-Key: story-11-merchant-approval
Content-Type: application/json
{
  "decision": "approve",
  "review_level": "enhanced",
  "reason": "reviewed VPN service and deterministic KYB approved"
}

HTTP 200
[Asserts]
jsonpath "$.status" == "approved"


POST {{base_url}}/admin/v1/merchant_services/{{merchant_service_id}}/decision
Authorization: Bearer {{admin_api_key}}
Idempotency-Key: story-11-service-approval
Content-Type: application/json
{
  "decision": "approve",
  "reason": "risk allow and sanctions clear"
}

HTTP 200
[Asserts]
jsonpath "$.status" == "approved"


POST {{base_url}}/admin/v1/merchant_services/{{blocked_service_id}}/decision
Authorization: Bearer {{admin_api_key}}
Idempotency-Key: story-11-blocked-service-override
Content-Type: application/json
{
  "decision": "approve",
  "reason": "attempted override must fail closed"
}

HTTP 409
[Asserts]
jsonpath "$.error.code" == "risk_denied"


GET {{base_url}}/account/v1/merchant_accounts/{{merchant_account_id}}/services
Authorization: Bearer {{active_user_one_key}}

HTTP 200
[Asserts]
jsonpath "$.data" count == 2
jsonpath "$.data[0].status" == "approved"
jsonpath "$.data[1].status" == "pending"


POST {{base_url}}/account/v1/accounts/21000000-0000-4000-8000-000000000002/topups
Authorization: Bearer {{active_user_two_key}}
Idempotency-Key: story-11-payer-topup
Content-Type: application/json
{
  "fiat_currency": "USD",
  "fiat_amount_minor": 900
}

HTTP 201
[Captures]
topup_id: jsonpath "$.id"
provider_reference: jsonpath "$.provider_reference"


POST {{payment_url}}/v1/test/confirm
Content-Type: application/json
{
  "callback_url": "{{base_url}}/callbacks/v1/payment_events",
  "event_id": "storypay-event-11-topup",
  "provider_reference": "{{provider_reference}}",
  "currency": "USD",
  "amount_minor": 900
}

HTTP 200


POST {{base_url}}/account/v1/accounts/21000000-0000-4000-8000-000000000002/api_keys
Authorization: Bearer {{active_user_two_key}}
Idempotency-Key: story-11-payer-gateway-key
Content-Type: application/json
{
  "name": "Launch payer AI",
  "kind": "gateway",
  "scopes": ["gateway:invoke", "gateway:usage:read"]
}

HTTP 201
[Captures]
payer_gateway_key: jsonpath "$.secret"


POST {{base_url}}/v1/chat/completions
Authorization: Bearer {{payer_gateway_key}}
Idempotency-Key: story-11-payer-ai
Content-Type: application/json
{
  "model": "story-launch",
  "messages": [{"role": "user", "content": "payer launch AI"}],
  "stream": false
}

HTTP 200
[Asserts]
jsonpath "$.model" == "story-launch"


POST {{base_url}}/account/v1/accounts/{{merchant_account_id}}/api_keys
Authorization: Bearer {{active_user_one_key}}
Idempotency-Key: story-11-merchant-payment-key
Content-Type: application/json
{
  "name": "Launch merchant payments",
  "kind": "payment",
  "scopes": ["pay:intents:write", "pay:transactions:read", "pay:webhooks:write"]
}

HTTP 201
[Captures]
merchant_payment_key: jsonpath "$.secret"


POST {{base_url}}/account/v1/accounts/{{merchant_account_id}}/api_keys
Authorization: Bearer {{active_user_one_key}}
Idempotency-Key: story-11-merchant-gateway-key
Content-Type: application/json
{
  "name": "Launch merchant AI",
  "kind": "gateway",
  "scopes": ["gateway:invoke", "gateway:usage:read"]
}

HTTP 201
[Captures]
merchant_gateway_key: jsonpath "$.secret"


POST {{base_url}}/pay/v1/webhook_endpoints
Authorization: Bearer {{merchant_payment_key}}
Idempotency-Key: story-11-create-merchant-webhook
Content-Type: application/json
{
  "url": "{{payment_url}}/merchant-webhook",
  "events": ["payment_intent.succeeded"]
}

HTTP 201


POST {{base_url}}/pay/v1/payment_intents
Authorization: Bearer {{merchant_payment_key}}
Idempotency-Key: story-11-order
Content-Type: application/json
{
  "service_id": "{{merchant_service_id}}",
  "external_order_id": "LAUNCH-VPN-0001",
  "amount": {"asset": "GIZ_CREDIT", "microcredits": 100001},
  "description": "Reviewed launch VPN subscription",
  "expires_at": "2027-08-10T00:00:00Z",
  "metadata": {"approved_service": "vpn"}
}

HTTP 201
[Captures]
payment_intent_id: jsonpath "$.id"
[Asserts]
jsonpath "$.platform_fee.microcredits" == 2501
jsonpath "$.net_amount.microcredits" == 97500
jsonpath "$.service_id" == "{{merchant_service_id}}"
jsonpath "$.service_code" == "vpn"


GET {{base_url}}/pay/v1/checkout/payment_intents/{{payment_intent_id}}
Authorization: Bearer {{active_user_two_key}}

HTTP 200
[Asserts]
jsonpath "$.description" == "Reviewed launch VPN subscription"
jsonpath "$.service_name" == "Launch VPN Subscription"


POST {{base_url}}/pay/v1/payment_intents/{{payment_intent_id}}/confirm
Authorization: Bearer {{active_user_two_key}}
Idempotency-Key: story-11-confirm

HTTP 200
[Asserts]
jsonpath "$.status" == "succeeded"


POST {{base_url}}/v1/chat/completions
Authorization: Bearer {{merchant_gateway_key}}
Idempotency-Key: story-11-merchant-ai
Content-Type: application/json
{
  "model": "story-launch",
  "messages": [{"role": "user", "content": "merchant spends earned Credit"}],
  "stream": false
}

HTTP 200


POST {{base_url}}/account/v1/accounts/21000000-0000-4000-8000-000000000002/topups/{{topup_id}}/refunds
Authorization: Bearer {{active_user_two_key}}
Idempotency-Key: story-11-refund
Content-Type: application/json
{
  "amount": {"asset": "GIZ_CREDIT", "microcredits": 5000000}
}

HTTP 202
[Asserts]
jsonpath "$.status" == "succeeded"
jsonpath "$.fiat_amount_minor" == 450


GET {{base_url}}/account/v1/accounts/21000000-0000-4000-8000-000000000002/balance
Authorization: Bearer {{active_user_two_key}}

HTTP 200
[Asserts]
jsonpath "$.microcredits" == 54899963


GET {{base_url}}/account/v1/accounts/{{merchant_account_id}}/balance
Authorization: Bearer {{active_user_one_key}}

HTTP 200
[Asserts]
jsonpath "$.microcredits" == 97464


GET {{base_url}}/account/v1/accounts/21000000-0000-4000-8000-000000000002/topups
Authorization: Bearer {{active_user_two_key}}

HTTP 200
[Asserts]
jsonpath "$.data[0].refundable_amount.microcredits" == 4899963


GET {{base_url}}/pay/v1/transactions
Authorization: Bearer {{merchant_payment_key}}

HTTP 200
[Asserts]
jsonpath "$.data" count == 1
jsonpath "$.data[0].net_amount.microcredits" == 97500


# Both AI calls must hit the endpoint created above. The process bootstrap
# provider is deliberately a different server; a non-zero count there would
# prove the Admin catalog was cosmetic rather than the routing source of truth.
GET {{catalog_fake_url}}/events

HTTP 200
[Asserts]
jsonpath "$.chat_calls" == 2


GET {{fake_url}}/events

HTTP 200
[Asserts]
jsonpath "$.chat_calls" == 0


GET {{payment_url}}/events

HTTP 200
[Asserts]
jsonpath "$.callbacks" == 1
jsonpath "$.refunds" == 1
jsonpath "$.webhooks" == 1


GET {{risk_url}}/events

HTTP 200
[Asserts]
jsonpath "$.assessments" == 2
# The replayed merchant-service command must be answered from Gizway's durable
# command record. Provider-side idempotency is a safety net, not a substitute:
# replaying the API command must not contact the risk provider a second time.
jsonpath "$.requests" == 2


GET {{base_url}}/admin/v1/payments
Authorization: Bearer {{admin_api_key}}

HTTP 200
[Asserts]
jsonpath "$.data" count == 3


GET {{base_url}}/admin/v1/gateway_requests
Authorization: Bearer {{admin_api_key}}

HTTP 200
[Asserts]
jsonpath "$.data" count == 2
