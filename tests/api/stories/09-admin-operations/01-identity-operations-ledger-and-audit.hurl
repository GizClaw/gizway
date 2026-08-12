# Story 09 — Administrator identity and controlled operational workflows.
#
# Product behavior:
# - Administrators may authenticate with a bcrypt-backed web login or a
#   separately stored Admin API Key; neither credential works on customer APIs.
# - There is one flat administrator authority model, but the final active
#   administrator cannot suspend or close itself.
# - User/merchant/key changes retain financial history and append an audit row
#   containing administrator, reason and resource.
# - Operations can inspect gateway/payment/ledger state without request or
#   credential secrets.
# - An adjustment must balance exactly. Reversal appends compensating entries;
#   PostgreSQL triggers make the original posted transaction immutable.
# - Webhook retry creates attempt 2 for the same event and never replays the
#   associated payment.
#
# Fixture: story-base includes one active admin and one failed outbox delivery.
# Isolation: fresh PostgreSQL per file.
# covers: gizpay-admin.yaml#loginAdministrator gizpay-admin.yaml#refreshAdministratorSession gizpay-admin.yaml#logoutAdministrator gizpay-admin.yaml#getCurrentAdministrator gizpay-admin.yaml#listAdministrators
# covers: gizpay-admin.yaml#createAdministrator gizpay-admin.yaml#getAdministrator gizpay-admin.yaml#updateAdministrator
# covers: gizpay-admin.yaml#listAdministratorApiKeys gizpay-admin.yaml#createAdministratorApiKey gizpay-admin.yaml#revokeAdministratorApiKey
# covers: gizpay-admin.yaml#getAdminOverview gizpay-admin.yaml#listUsers gizpay-admin.yaml#getUser gizpay-admin.yaml#changeUserStatus
# covers: gizpay-admin.yaml#listMerchants gizpay-admin.yaml#getMerchant gizpay-admin.yaml#decideMerchantReview
# covers: gizpay-admin.yaml#listAdminApiKeys gizpay-admin.yaml#adminRevokeApiKey gizway-admin.yaml#listGatewayExecutions gizway-admin.yaml#getGatewayExecution
# covers: gizpay-admin.yaml#listAdminPayments gizpay-admin.yaml#listLedgerAccounts gizpay-admin.yaml#changeAccountBalanceStatus gizpay-admin.yaml#listLedgerTransactions
# covers: gizpay-admin.yaml#createLedgerAdjustment gizpay-admin.yaml#reverseLedgerTransaction
# covers: gizpay-admin.yaml#listWebhookDeliveries gizpay-admin.yaml#retryWebhookDelivery gizpay-admin.yaml#listAuditEvents

POST {{pay_url}}/admin/v1/auth/login
Idempotency-Key: story-09-admin-login
Content-Type: application/json
{
  "email": "admin@gizway.test",
  "password": "story-admin-password"
}

HTTP 200
[Captures]
admin_session: jsonpath "$.access_token"
[Asserts]
jsonpath "$.administrator.id" == "41000000-0000-4000-8000-000000000001"
jsonpath "$.token_type" == "Bearer"


# Login is also a durable command. The exact retry returns the same session
# token; its replay body is encrypted at rest with the process secret key.
POST {{pay_url}}/admin/v1/auth/login
Idempotency-Key: story-09-admin-login
Content-Type: application/json
{
  "email": "admin@gizway.test",
  "password": "story-admin-password"
}

HTTP 200
[Asserts]
jsonpath "$.access_token" == "{{admin_session}}"


GET {{pay_url}}/admin/v1/me
Authorization: Bearer {{admin_session}}

HTTP 200
[Asserts]
jsonpath "$.email" == "admin@gizway.test"


POST {{pay_url}}/admin/v1/administrators
Authorization: Bearer {{admin_session}}
Idempotency-Key: story-09-create-admin
Content-Type: application/json
{
  "email": "second-admin@gizway.test",
  "display_name": "Second Administrator",
  "password": "second-admin-password"
}

HTTP 201
[Captures]
second_admin_id: jsonpath "$.id"


GET {{pay_url}}/admin/v1/administrators
Authorization: Bearer {{admin_api_key}}

HTTP 200
[Asserts]
jsonpath "$.data" count == 2


GET {{pay_url}}/admin/v1/administrators/{{second_admin_id}}
Authorization: Bearer {{admin_api_key}}

HTTP 200
[Asserts]
jsonpath "$.status" == "active"


# Password is a real PATCH field, not a silently ignored OpenAPI decoration.
# Rotation updates bcrypt state atomically, revokes sessions issued by the old
# authenticator, rejects the old password, and accepts the new password.
POST {{pay_url}}/admin/v1/auth/login
Idempotency-Key: story-09-second-admin-old-login
Content-Type: application/json
{
  "email": "second-admin@gizway.test",
  "password": "second-admin-password"
}

HTTP 200
[Captures]
second_admin_session: jsonpath "$.access_token"


PATCH {{pay_url}}/admin/v1/administrators/{{second_admin_id}}
Authorization: Bearer {{admin_api_key}}
Idempotency-Key: story-09-rotate-second-admin-password
Content-Type: application/json
{
  "password": "second-admin-password-rotated",
  "reason": "scheduled password rotation"
}

HTTP 200


GET {{pay_url}}/admin/v1/me
Authorization: Bearer {{second_admin_session}}

HTTP 401


POST {{pay_url}}/admin/v1/auth/login
Idempotency-Key: story-09-second-admin-old-password-rejected
Content-Type: application/json
{
  "email": "second-admin@gizway.test",
  "password": "second-admin-password"
}

HTTP 401


POST {{pay_url}}/admin/v1/auth/login
Idempotency-Key: story-09-second-admin-new-password
Content-Type: application/json
{
  "email": "second-admin@gizway.test",
  "password": "second-admin-password-rotated"
}

HTTP 200
[Asserts]
jsonpath "$.administrator.id" == "{{second_admin_id}}"


POST {{pay_url}}/admin/v1/administrators/{{second_admin_id}}/api_keys
Authorization: Bearer {{admin_api_key}}
Idempotency-Key: story-09-second-admin-key
Content-Type: application/json
{
  "name": "Second admin automation"
}

HTTP 201
[Captures]
second_admin_key_id: jsonpath "$.id"
[Asserts]
jsonpath "$.secret" startsWith "gizadm_"


GET {{pay_url}}/admin/v1/administrators/{{second_admin_id}}/api_keys
Authorization: Bearer {{admin_api_key}}

HTTP 200
[Asserts]
jsonpath "$.data" count == 1
jsonpath "$.data[0].secret" not exists


POST {{pay_url}}/admin/v1/administrators/{{second_admin_id}}/api_keys/{{second_admin_key_id}}/revoke
Authorization: Bearer {{admin_api_key}}
Idempotency-Key: story-09-revoke-admin-key
Content-Type: application/json
{
  "reason": "rotation"
}

HTTP 204


PATCH {{pay_url}}/admin/v1/administrators/{{second_admin_id}}
Authorization: Bearer {{admin_api_key}}
Idempotency-Key: story-09-suspend-second-admin
Content-Type: application/json
{
  "display_name": "Second Administrator Suspended",
  "status": "suspended",
  "reason": "exercise lifecycle"
}

HTTP 200
[Asserts]
jsonpath "$.status" == "suspended"


# With the second admin suspended, closing the only remaining active admin is
# rejected so operations cannot lock themselves out.
PATCH {{pay_url}}/admin/v1/administrators/41000000-0000-4000-8000-000000000001
Authorization: Bearer {{admin_api_key}}
Idempotency-Key: story-09-reject-final-admin-suspend
Content-Type: application/json
{
  "status": "closed",
  "reason": "must be rejected"
}

HTTP 409
[Asserts]
jsonpath "$.error.code" == "last_active_administrator"


GET {{pay_url}}/admin/v1/users
Authorization: Bearer {{admin_api_key}}

HTTP 200
[Asserts]
jsonpath "$.data" count == 3


GET {{pay_url}}/admin/v1/users/11000000-0000-4000-8000-000000000003
Authorization: Bearer {{admin_api_key}}

HTTP 200
[Asserts]
jsonpath "$.status" == "suspended"
jsonpath "$.accounts" count == 1


POST {{pay_url}}/admin/v1/users/11000000-0000-4000-8000-000000000003/status
Authorization: Bearer {{admin_api_key}}
Idempotency-Key: story-09-reactivate-user
Content-Type: application/json
{
  "status": "active",
  "reason": "review completed"
}

HTTP 200
[Asserts]
jsonpath "$.status" == "active"


POST {{pay_url}}/account/v1/merchant_accounts
Authorization: Bearer {{active_user_one_key}}
Idempotency-Key: story-09-merchant-application
Content-Type: application/json
{
  "name": "Admin Review Merchant",
  "legal_name": "Admin Review Merchant LLC",
  "public_name": "Admin Review",
  "country_code": "US"
}

HTTP 201
[Captures]
review_account_id: jsonpath "$.account.id"


GET {{pay_url}}/admin/v1/merchants/{{review_account_id}}
Authorization: Bearer {{admin_api_key}}

HTTP 200
[Asserts]
jsonpath "$.status" == "pending"


POST {{pay_url}}/admin/v1/merchants/{{review_account_id}}/decision
Authorization: Bearer {{admin_api_key}}
Idempotency-Key: story-09-approve-merchant
Content-Type: application/json
{
  "decision": "approve",
  "review_level": "enhanced",
  "reason": "deterministic KYB fixture approved"
}

HTTP 200
[Asserts]
jsonpath "$.status" == "approved"


GET {{pay_url}}/admin/v1/merchants
Authorization: Bearer {{admin_api_key}}

HTTP 200
[Asserts]
jsonpath "$.data" count == 2


# Generate one real gateway request for diagnostic inspection.
POST {{way_url}}/v1/chat/completions
Authorization: Bearer {{gateway_api_key}}
Idempotency-Key: story-09-admin-diagnostic-call
Content-Type: application/json
{
  "model": "story-text",
  "messages": [{"role": "user", "content": "admin diagnostic"}],
  "stream": false
}

HTTP 200


GET {{way_url}}/admin/v1/gateway_executions
Authorization: Bearer {{admin_api_key}}

HTTP 200
[Captures]
execution_id: jsonpath "$.data[0].id"
[Asserts]
jsonpath "$.data" count == 1
jsonpath "$.data[0].payload_hash" not exists
jsonpath "$.data[0].response_json" not exists


GET {{way_url}}/admin/v1/gateway_executions/{{execution_id}}
Authorization: Bearer {{admin_api_key}}

HTTP 200
[Asserts]
jsonpath "$.actual_microcredits" == 36


POST {{pay_url}}/account/v1/accounts/21000000-0000-4000-8000-000000000001/transfers
Authorization: Bearer {{active_user_one_key}}
Idempotency-Key: story-09-visible-transfer
Content-Type: application/json
{
  "recipient_account_id": "21000000-0000-4000-8000-000000000002",
  "amount": {"asset": "GIZ_CREDIT", "microcredits": 100},
  "note": "admin payment view"
}

HTTP 201


GET {{pay_url}}/admin/v1/payments
Authorization: Bearer {{admin_api_key}}

HTTP 200
[Asserts]
jsonpath "$.data" count == 1
jsonpath "$.data[0].type" == "transfer"


GET {{pay_url}}/admin/v1/api_keys
Authorization: Bearer {{admin_api_key}}

HTTP 200
[Asserts]
jsonpath "$.data" count == 3
jsonpath "$.data[0].secret_hash" not exists


POST {{pay_url}}/admin/v1/api_keys/31000000-0000-4000-8000-000000000002/revoke
Authorization: Bearer {{admin_api_key}}
Idempotency-Key: story-09-revoke-customer-key
Content-Type: application/json
{
  "reason": "compromised fixture key"
}

HTTP 204


GET {{pay_url}}/admin/v1/ledger/accounts
Authorization: Bearer {{admin_api_key}}

HTTP 200
[Asserts]
jsonpath "$.data" count == 7


# Freeze changes central spendability state, never posted history. A Gateway
# may use the remainder of its already returned short quota window; the next
# quota query observes the freeze and stops new execution.
POST {{pay_url}}/admin/v1/accounts/21000000-0000-4000-8000-000000000001/balance_status
Authorization: Bearer {{admin_api_key}}
Idempotency-Key: story-09-freeze-balance
X-Request-ID: story-09-freeze-request
Content-Type: application/json
{
  "status": "frozen",
  "reason": "investigating an anomalous spend pattern"
}

HTTP 200
[Asserts]
jsonpath "$.status" == "frozen"


GET {{pay_url}}/account/v1/accounts/21000000-0000-4000-8000-000000000001/balance
Authorization: Bearer {{active_user_one_key}}

HTTP 200
[Asserts]
jsonpath "$.microcredits" exists


POST {{pay_url}}/account/v1/accounts/21000000-0000-4000-8000-000000000001/transfers
Authorization: Bearer {{active_user_one_key}}
Idempotency-Key: story-09-frozen-transfer
Content-Type: application/json
{
  "recipient_account_id": "21000000-0000-4000-8000-000000000002",
  "amount": {"asset": "GIZ_CREDIT", "microcredits": 1},
  "note": "must not spend frozen Credit"
}

HTTP 423
[Asserts]
jsonpath "$.error.code" == "account_frozen"


POST {{way_url}}/v1/chat/completions
Authorization: Bearer {{gateway_api_key}}
Idempotency-Key: story-09-frozen-ai
Content-Type: application/json
{
  "model": "story-text",
  "messages": [{"role": "user", "content": "must not reserve frozen Credit"}],
  "stream": false
}

HTTP 200


POST {{way_url}}/test/v1/clock/advance
Content-Type: application/json
{
  "by": "5m"
}

HTTP 200


POST {{way_url}}/v1/chat/completions
Authorization: Bearer {{gateway_api_key}}
Idempotency-Key: story-09-frozen-ai-after-quota-refresh
Content-Type: application/json
{
  "model": "story-text",
  "messages": [{"role": "user", "content": "fresh quota must observe the frozen balance"}],
  "stream": false
}

HTTP 402
[Asserts]
jsonpath "$.error.code" == "quota_denied"


# The fresh denied quota answer is shared by compatible protocols. Responses
# rejects locally before Bifrost or an upstream endpoint is called.
POST {{way_url}}/v1/responses
Authorization: Bearer {{gateway_api_key}}
Idempotency-Key: story-09-frozen-responses
Content-Type: application/json
{
  "model": "story-text",
  "input": "must not reserve frozen Credit through Responses"
}

HTTP 402
[Asserts]
jsonpath "$.error.code" == "quota_denied"


POST {{pay_url}}/admin/v1/accounts/21000000-0000-4000-8000-000000000001/balance_status
Authorization: Bearer {{admin_api_key}}
Idempotency-Key: story-09-unfreeze-balance
Content-Type: application/json
{
  "status": "active",
  "reason": "investigation completed"
}

HTTP 200
[Asserts]
jsonpath "$.status" == "active"


# Generic ledger reversal is only valid for an administrator adjustment. A
# top-up also owns a refundable Credit lot; reversing only its journal entries
# would leave that lot refundable and could spend an active reservation twice.
# Product transactions must use their domain compensation workflow instead.
POST {{pay_url}}/admin/v1/ledger/transactions/c1000000-0000-4000-8000-000000000001/reverse
Authorization: Bearer {{admin_api_key}}
Idempotency-Key: story-09-reject-topup-reversal
Content-Type: application/json
{
  "reason": "must go through the refund workflow"
}

HTTP 409
[Asserts]
jsonpath "$.error.code" == "idempotency_conflict"


POST {{pay_url}}/admin/v1/ledger/adjustments
Authorization: Bearer {{admin_api_key}}
Idempotency-Key: story-09-adjustment
Content-Type: application/json
{
  "description": "Ten microcredit support adjustment",
  "reason": "verified support correction",
  "entries": [
    {"ledger_account_id": "b1000000-0000-4000-8000-000000000004", "direction": "debit", "amount_microcredits": 10},
    {"ledger_account_id": "b1000000-0000-4000-8000-000000000001", "direction": "credit", "amount_microcredits": 10}
  ]
}

HTTP 201
[Captures]
adjustment_id: jsonpath "$.id"
[Asserts]
jsonpath "$.entries" count == 2


# Same payload/key replays the exact committed result instead of attempting a
# second ledger transaction. A changed payload conflicts at the generic atomic
# command journal before any entry can be posted.
POST {{pay_url}}/admin/v1/ledger/adjustments
Authorization: Bearer {{admin_api_key}}
Idempotency-Key: story-09-adjustment
Content-Type: application/json
{
  "description": "Ten microcredit support adjustment",
  "reason": "verified support correction",
  "entries": [
    {"ledger_account_id": "b1000000-0000-4000-8000-000000000004", "direction": "debit", "amount_microcredits": 10},
    {"ledger_account_id": "b1000000-0000-4000-8000-000000000001", "direction": "credit", "amount_microcredits": 10}
  ]
}

HTTP 201
[Asserts]
jsonpath "$.id" == "{{adjustment_id}}"


POST {{pay_url}}/admin/v1/ledger/adjustments
Authorization: Bearer {{admin_api_key}}
Idempotency-Key: story-09-adjustment
Content-Type: application/json
{
  "description": "Changed payload must conflict",
  "reason": "verified support correction",
  "entries": [
    {"ledger_account_id": "b1000000-0000-4000-8000-000000000004", "direction": "debit", "amount_microcredits": 11},
    {"ledger_account_id": "b1000000-0000-4000-8000-000000000001", "direction": "credit", "amount_microcredits": 11}
  ]
}

HTTP 409
[Asserts]
jsonpath "$.error.code" == "idempotency_conflict"


# Checked accumulation rejects a mathematically unbalanced adjustment even
# when naive int64 addition would wrap both sides to the same machine value.
POST {{pay_url}}/admin/v1/ledger/adjustments
Authorization: Bearer {{admin_api_key}}
Idempotency-Key: story-09-adjustment-overflow
Content-Type: application/json
{
  "description": "Overflow must not look balanced",
  "reason": "integer boundary contract",
  "entries": [
    {"ledger_account_id": "b1000000-0000-4000-8000-000000000004", "direction": "debit", "amount_microcredits": 9223372036854775807},
    {"ledger_account_id": "b1000000-0000-4000-8000-000000000004", "direction": "debit", "amount_microcredits": 9223372036854775807},
    {"ledger_account_id": "b1000000-0000-4000-8000-000000000004", "direction": "debit", "amount_microcredits": 9223372036854775807},
    {"ledger_account_id": "b1000000-0000-4000-8000-000000000001", "direction": "credit", "amount_microcredits": 9223372036854775805}
  ]
}

HTTP 400
[Asserts]
jsonpath "$.error.code" == "unbalanced_adjustment"


POST {{pay_url}}/admin/v1/ledger/transactions/{{adjustment_id}}/reverse
Authorization: Bearer {{admin_api_key}}
Idempotency-Key: story-09-reverse-adjustment
Content-Type: application/json
{
  "reason": "support correction no longer needed"
}

HTTP 201
[Captures]
reversal_id: jsonpath "$.id"
[Asserts]
jsonpath "$.transaction_type" == "reversal"
jsonpath "$.entries[0].direction" == "credit"
jsonpath "$.entries[1].direction" == "debit"


# A retry key cannot silently change the audited reason/body.
POST {{pay_url}}/admin/v1/ledger/transactions/{{adjustment_id}}/reverse
Authorization: Bearer {{admin_api_key}}
Idempotency-Key: story-09-reverse-adjustment
Content-Type: application/json
{
  "reason": "changed reason must conflict"
}

HTTP 409
[Asserts]
jsonpath "$.error.code" == "idempotency_conflict"


# The original economic transaction, not only the transport key, is the
# reversal idempotency boundary. A second key must replay the one compensating
# transaction instead of applying the entries again.
POST {{pay_url}}/admin/v1/ledger/transactions/{{adjustment_id}}/reverse
Authorization: Bearer {{admin_api_key}}
Idempotency-Key: story-09-reverse-adjustment-different-key
Content-Type: application/json
{
  "reason": "duplicate operator retry"
}

HTTP 201
[Asserts]
jsonpath "$.id" == "{{reversal_id}}"


GET {{pay_url}}/admin/v1/ledger/transactions?transaction_type=reversal
Authorization: Bearer {{admin_api_key}}

HTTP 200
[Asserts]
jsonpath "$.data" count == 1
jsonpath "$.data[0].id" == "{{reversal_id}}"


# Build a failed webhook delivery through public product APIs so the Admin
# retry story does not depend on a hidden database row in the shared fixture.
POST {{pay_url}}/account/v1/accounts/22000000-0000-4000-8000-000000000002/api_keys
Authorization: Bearer {{active_user_two_key}}
Idempotency-Key: story-09-payment-key
Content-Type: application/json
{
  "name": "Admin retry setup",
  "kind": "payment",
  "scopes": ["pay:intents:write", "pay:webhooks:write"]
}

HTTP 201
[Captures]
retry_payment_key: jsonpath "$.secret"


POST {{pay_url}}/pay/v1/webhook_endpoints
Authorization: Bearer {{retry_payment_key}}
Idempotency-Key: story-09-failing-webhook
Content-Type: application/json
{
  "url": "http://127.0.0.1:1/unreachable",
  "events": ["payment_intent.succeeded"]
}

HTTP 201


POST {{pay_url}}/pay/v1/payment_intents
Authorization: Bearer {{retry_payment_key}}
Idempotency-Key: story-09-webhook-payment
Content-Type: application/json
{
  "service_id": "23000000-0000-4000-8000-000000000001",
  "external_order_id": "STORY-09-WEBHOOK",
  "amount": {"asset": "GIZ_CREDIT", "microcredits": 1000},
  "description": "Create retryable delivery",
  "expires_at": "2027-08-12T00:00:00Z",
  "metadata": {}
}

HTTP 201
[Captures]
retry_payment_intent_id: jsonpath "$.id"


POST {{pay_url}}/pay/v1/payment_intents/{{retry_payment_intent_id}}/confirm
Authorization: Bearer {{active_user_one_key}}
Idempotency-Key: story-09-confirm-webhook-payment

HTTP 200


GET {{pay_url}}/admin/v1/webhook_deliveries
Authorization: Bearer {{admin_api_key}}
[Options]
retry: 30
retry-interval: 100ms

HTTP 200
[Captures]
failed_delivery_id: jsonpath "$.data[0].id"
[Asserts]
jsonpath "$.data" count == 2
jsonpath "$.data[0].attempt" == 1
jsonpath "$.data[0].status" == "failed"
jsonpath "$.data[1].attempt" == 2
jsonpath "$.data[1].status" == "pending"


# A delivery failure durably creates the next pending attempt. Advance the
# GizPay clock through the bounded 1s, 2s, 4s, and 8s backoff windows. The
# dispatcher may run between HTTP requests, so each observation retries until
# the expected committed attempt is visible.
POST {{pay_url}}/test/v1/clock/advance
Content-Type: application/json
{"by":"2s"}

HTTP 200


GET {{pay_url}}/admin/v1/webhook_deliveries
Authorization: Bearer {{admin_api_key}}
[Options]
retry: 30
retry-interval: 100ms

HTTP 200
[Asserts]
jsonpath "$.data" count == 3
jsonpath "$.data[1].status" == "failed"
jsonpath "$.data[2].attempt" == 3
jsonpath "$.data[2].status" == "pending"


POST {{pay_url}}/test/v1/clock/advance
Content-Type: application/json
{"by":"4s"}

HTTP 200


GET {{pay_url}}/admin/v1/webhook_deliveries
Authorization: Bearer {{admin_api_key}}
[Options]
retry: 30
retry-interval: 100ms

HTTP 200
[Asserts]
jsonpath "$.data" count == 4
jsonpath "$.data[2].status" == "failed"
jsonpath "$.data[3].attempt" == 4
jsonpath "$.data[3].status" == "pending"


POST {{pay_url}}/test/v1/clock/advance
Content-Type: application/json
{"by":"8s"}

HTTP 200


GET {{pay_url}}/admin/v1/webhook_deliveries
Authorization: Bearer {{admin_api_key}}
[Options]
retry: 30
retry-interval: 100ms

HTTP 200
[Asserts]
jsonpath "$.data" count == 5
jsonpath "$.data[3].status" == "failed"
jsonpath "$.data[4].attempt" == 5
jsonpath "$.data[4].status" == "pending"


POST {{pay_url}}/test/v1/clock/advance
Content-Type: application/json
{"by":"16s"}

HTTP 200


GET {{pay_url}}/admin/v1/webhook_deliveries
Authorization: Bearer {{admin_api_key}}
[Options]
retry: 30
retry-interval: 100ms

HTTP 200
[Asserts]
jsonpath "$.data" count == 5
jsonpath "$.data[4].attempt" == 5
jsonpath "$.data[4].status" == "exhausted"


POST {{pay_url}}/admin/v1/webhook_deliveries/{{failed_delivery_id}}/retry
Authorization: Bearer {{admin_api_key}}
Idempotency-Key: story-09-retry-webhook

HTTP 202
[Captures]
retried_delivery_id: jsonpath "$.delivery_id"


# The administrator command itself is durable and idempotent. Replaying the
# same key returns the exact delivery identity; it must not create another
# outbox row or another audit event.
POST {{pay_url}}/admin/v1/webhook_deliveries/{{failed_delivery_id}}/retry
Authorization: Bearer {{admin_api_key}}
Idempotency-Key: story-09-retry-webhook

HTTP 202
[Asserts]
jsonpath "$.delivery_id" == "{{retried_delivery_id}}"


GET {{pay_url}}/admin/v1/webhook_deliveries
Authorization: Bearer {{admin_api_key}}
[Options]
retry: 30
retry-interval: 100ms

HTTP 200
[Asserts]
# The manual command continues the existing sequence at attempt 6. Because the
# retry budget is already exhausted, its deterministic failure is terminal and
# the idempotent command replay above cannot create a seventh row.
jsonpath "$.data" count == 6
jsonpath "$.data[5].id" == "{{retried_delivery_id}}"
jsonpath "$.data[5].attempt" == 6
jsonpath "$.data[5].status" == "exhausted"


GET {{pay_url}}/admin/v1/audit_events
Authorization: Bearer {{admin_api_key}}

HTTP 200
[Asserts]
jsonpath "$.data" count >= 8


GET {{pay_url}}/admin/v1/overview
Authorization: Bearer {{admin_api_key}}

HTTP 200
[Asserts]
jsonpath "$.users" == 3
# GizPay reports the Usage records exchanged by regional Gateways; regional
# request execution state remains in GizWay and is not projected into GizPay.
jsonpath "$.received_usage" == 2
jsonpath "$.charged.microcredits" == 72


# Administrator sessions use the same explicit rotation and revocation
# contract as User sessions. Admin API Keys are a separate credential class and
# cannot be refreshed or logged out through these routes.
POST {{pay_url}}/admin/v1/auth/refresh
Authorization: Bearer {{admin_session}}
Idempotency-Key: story-09-refresh-admin-session

HTTP 200
[Captures]
refreshed_admin_session: jsonpath "$.access_token"
[Asserts]
jsonpath "$.administrator.id" == "41000000-0000-4000-8000-000000000001"
jsonpath "$.csrf_protection" == "not_required_bearer_header"
jsonpath "$.access_token" != "{{admin_session}}"


GET {{pay_url}}/admin/v1/me
Authorization: Bearer {{admin_session}}

HTTP 401


POST {{pay_url}}/admin/v1/auth/logout
Authorization: Bearer {{refreshed_admin_session}}
Idempotency-Key: story-09-logout-admin-session

HTTP 204


GET {{pay_url}}/admin/v1/me
Authorization: Bearer {{refreshed_admin_session}}

HTTP 401
