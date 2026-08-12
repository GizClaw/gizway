# Compose product profile: real GizPay/GizWay binaries and separate PostgreSQL
# databases exercise customer identity, API-key management, purchased Credit,
# AI Usage, transfer, original-route refund, and merchant payment in one loop.

POST {{global_url}}/admin/v1/auth/login
Idempotency-Key: e2e-product-global-login
Content-Type: application/json
{
  "email": "global-admin@e2e.invalid",
  "password": "e2e-password"
}
HTTP 200
[Captures]
global_admin: jsonpath "$.access_token"

POST {{global_url}}/admin/v1/rate_publications
Authorization: Bearer {{global_admin}}
Idempotency-Key: e2e-product-publication
Content-Type: application/json
{
  "source_publication_id": "e2e-product-publication",
  "effective_at": "2026-08-11T00:00:00Z"
}
HTTP 201

GET {{pay_url}}/account/v1/me
Authorization: Bearer gzs_e2e_customer_two_session
HTTP 200
[Asserts]
jsonpath "$.id" == "e2e-user-two"

GET {{pay_url}}/account/v1/accounts
Authorization: Bearer gzs_e2e_customer_two_session
HTTP 200
[Asserts]
jsonpath "$.data[0].id" == "e2e-account-two"

POST {{pay_url}}/account/v1/accounts/e2e-account-two/api_keys
Authorization: Bearer gzs_e2e_customer_two_session
Idempotency-Key: e2e-product-gateway-key
Content-Type: application/json
{
  "name": "Compose product Gateway",
  "kind": "gateway",
  "scopes": ["gateway:invoke", "gateway:usage:read"]
}
HTTP 201
[Captures]
gateway_key: jsonpath "$.secret"

POST {{pay_url}}/account/v1/accounts/e2e-account-two/topups
Authorization: Bearer gzs_e2e_customer_two_session
Idempotency-Key: e2e-product-topup
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
  "callback_url": "{{pay_url}}/callbacks/v1/payment_events",
  "event_id": "e2e-product-topup-event",
  "provider_reference": "{{provider_reference}}",
  "currency": "USD",
  "amount_minor": 900
}
HTTP 200

POST {{pay_url}}/account/v1/accounts/e2e-account-two/transfers
Authorization: Bearer gzs_e2e_customer_two_session
Idempotency-Key: e2e-product-transfer
Content-Type: application/json
{
  "recipient_account_id": "e2e-account",
  "amount": {"asset": "GIZ_CREDIT", "microcredits": 1000},
  "note": "Compose product transfer"
}
HTTP 201

POST {{global_url}}/v1/responses
Authorization: Bearer {{gateway_key}}
Content-Type: application/json
{
  "model": "global-model",
  "input": "Compose product AI"
}
HTTP 200

# The profile's one-second quota window has elapsed because Hurl delays every
# request. Discovery triggers the next Exchange and reports the AI Usage.
GET {{global_url}}/v1/models
Authorization: Bearer {{gateway_key}}
HTTP 200

GET {{pay_url}}/account/v1/accounts/e2e-account-two/usage?from=2026-08-12T00%3A00%3A00Z&to=2026-08-13T00%3A00%3A00Z
Authorization: Bearer gzs_e2e_customer_two_session
HTTP 200
[Asserts]
jsonpath "$.data" count == 1

# The merchant fixture contains identity and an approved merchant account only.
# Its service and risk decision must be created through the real GizPay -> Risk
# Provider path; bootstrap is deliberately forbidden from inserting either row.
POST {{pay_url}}/account/v1/merchant_accounts/e2e-merchant/services
Authorization: Bearer gzs_e2e_customer_session
Idempotency-Key: e2e-product-merchant-service
Content-Type: application/json
{
  "service_code": "e2e-product",
  "name": "E2E Product Service",
  "description": "Created through mandatory Compose risk screening",
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

# An idempotent replay must be answered by GizPay without calling Risk twice.
POST {{pay_url}}/account/v1/merchant_accounts/e2e-merchant/services
Authorization: Bearer gzs_e2e_customer_session
Idempotency-Key: e2e-product-merchant-service
Content-Type: application/json
{
  "service_code": "e2e-product",
  "name": "E2E Product Service",
  "description": "Created through mandatory Compose risk screening",
  "interface_set": ["checkout", "webhook"]
}
HTTP 200

GET {{risk_url}}/events
HTTP 200
[Asserts]
jsonpath "$.requests" == 1
jsonpath "$.assessments" == 1

POST {{pay_url}}/admin/v1/auth/login
Idempotency-Key: e2e-product-pay-admin-login
Content-Type: application/json
{
  "email": "admin@e2e.invalid",
  "password": "e2e-password"
}
HTTP 200
[Captures]
pay_admin: jsonpath "$.access_token"

POST {{pay_url}}/admin/v1/merchant_services/{{merchant_service_id}}/decision
Authorization: Bearer {{pay_admin}}
Idempotency-Key: e2e-product-approve-service
Content-Type: application/json
{
  "decision": "approve",
  "reason": "Risk allow decision verified by Product E2E"
}
HTTP 200
[Asserts]
jsonpath "$.status" == "approved"

POST {{pay_url}}/account/v1/accounts/e2e-account-two/topups/{{topup_id}}/refunds
Authorization: Bearer gzs_e2e_customer_two_session
Idempotency-Key: e2e-product-refund
Content-Type: application/json
{
  "amount": {"asset": "GIZ_CREDIT", "microcredits": 1000000}
}
HTTP 202
[Asserts]
jsonpath "$.status" == "succeeded"

POST {{pay_url}}/account/v1/accounts/e2e-merchant/api_keys
Authorization: Bearer gzs_e2e_customer_session
Idempotency-Key: e2e-product-payment-key
Content-Type: application/json
{
  "name": "Compose merchant Payment",
  "kind": "payment",
  "scopes": ["pay:intents:write", "pay:transactions:read"]
}
HTTP 201
[Captures]
payment_key: jsonpath "$.secret"

POST {{pay_url}}/pay/v1/payment_intents
Authorization: Bearer {{payment_key}}
Idempotency-Key: e2e-product-payment
Content-Type: application/json
{
  "service_id": "{{merchant_service_id}}",
  "external_order_id": "E2E-PRODUCT-ORDER",
  "amount": {"asset": "GIZ_CREDIT", "microcredits": 100001},
  "description": "Compose merchant payment",
  "expires_at": "2027-08-12T00:00:00Z",
  "metadata": {}
}
HTTP 201
[Captures]
payment_intent_id: jsonpath "$.id"

GET {{pay_url}}/pay/v1/checkout/payment_intents/{{payment_intent_id}}
Authorization: Bearer gzs_e2e_customer_two_session
HTTP 200

POST {{pay_url}}/pay/v1/payment_intents/{{payment_intent_id}}/confirm
Authorization: Bearer gzs_e2e_customer_two_session
Idempotency-Key: e2e-product-confirm
HTTP 200
[Asserts]
jsonpath "$.status" == "succeeded"

GET {{pay_url}}/pay/v1/transactions
Authorization: Bearer {{payment_key}}
HTTP 200
[Asserts]
jsonpath "$.data" count == 1
