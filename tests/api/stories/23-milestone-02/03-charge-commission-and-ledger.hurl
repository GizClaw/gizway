# Milestone 02 — Charge, Commission, fixed fee, ledger, and explicit duplicate conflict.
# covers: internal-gizpay.yaml#createPAYGCharge internal-gizpay.yaml#getPAYGCharge
# covers: account.yaml#getAccountBalance account.yaml#listAccountTransactions account.yaml#listAccountCharges

POST {{pay_url}}/service/v1/payg-charges
Authorization: Bearer {{service_token}}
Content-Type: application/json
{
  "external_order_id": "ord_m02_charge_0001",
  "api_key_hmac": "{{active_subscription_hmac}}",
  "gross_microcredits": 100,
  "commissions": [
    {
      "merchant_id": "{{provider_merchant_id}}",
      "amount_microcredits": 80
    }
  ],
  "order": {
    "type": "ai_call",
    "title": "Milestone 02 call",
    "model": "story-text",
    "provider": "fake",
    "provider_reference": "provider-request-0001"
  },
  "service_started_at": "2026-08-13T12:00:00Z",
  "service_completed_at": "2026-08-13T12:00:03Z"
}

HTTP 201
[Captures]
charge_id: jsonpath "$.charge_id"
ledger_transaction_id: jsonpath "$.ledger_transaction_id"
[Asserts]
jsonpath "$.external_order_id" == "ord_m02_charge_0001"
jsonpath "$.gross_microcredits" == 100
jsonpath "$.platform_fee_microcredits" == 2
jsonpath "$.main_merchant_net_microcredits" == 18
jsonpath "$.commissions[0].amount_microcredits" == 80


GET {{pay_url}}/service/v1/payg-charges/ord_m02_charge_0001
Authorization: Bearer {{service_token_other_user}}

HTTP 200
[Asserts]
jsonpath "$.charge_id" == "{{charge_id}}"
jsonpath "$.ledger_transaction_id" == "{{ledger_transaction_id}}"


# Even a byte-for-byte logical retry from a replacement Service Account is
# 409. The caller identity is audit data and never part of the unique key.
POST {{pay_url}}/service/v1/payg-charges
Authorization: Bearer {{service_token_rotated}}
Content-Type: application/json
{
  "external_order_id": "ord_m02_charge_0001",
  "api_key_hmac": "{{active_subscription_hmac}}",
  "gross_microcredits": 100,
  "commissions": [
    {
      "merchant_id": "{{provider_merchant_id}}",
      "amount_microcredits": 80
    }
  ],
  "order": {
    "type": "ai_call",
    "title": "Milestone 02 call",
    "model": "story-text",
    "provider": "fake",
    "provider_reference": "provider-request-0001"
  },
  "service_started_at": "2026-08-13T12:00:00Z",
  "service_completed_at": "2026-08-13T12:00:03Z"
}

HTTP 409
[Asserts]
jsonpath "$.error.code" == "duplicate_external_order_id"
body not contains "{{ledger_transaction_id}}"


# Changed content under the same ID is the same duplicate error; the service
# does not canonicalize or compare amount, metadata, timestamp, or ordering.
POST {{pay_url}}/service/v1/payg-charges
Authorization: Bearer {{service_token}}
Content-Type: application/json
{
  "external_order_id": "ord_m02_charge_0001",
  "api_key_hmac": "{{active_subscription_hmac}}",
  "gross_microcredits": 999,
  "commissions": [],
  "order": {"type":"ai_call","title":"different request"},
  "service_started_at": "2026-08-13T13:00:00Z",
  "service_completed_at": "2026-08-13T13:00:01Z"
}

HTTP 409
[Asserts]
jsonpath "$.error.code" == "duplicate_external_order_id"


# A revoked Key can still locate the durable Subscription for a delayed Charge.
POST {{pay_url}}/service/v1/payg-charges
Authorization: Bearer {{service_token_other_user}}
Content-Type: application/json
{
  "external_order_id": "ord_m02_revoked_key_late_charge",
  "api_key_hmac": "{{revoked_subscription_hmac}}",
  "gross_microcredits": 50,
  "commissions": [],
  "order": {"type":"ai_call","title":"late report"},
  "service_started_at": "2026-08-13T12:04:00Z",
  "service_completed_at": "2026-08-13T12:04:10Z"
}

HTTP 201
[Asserts]
jsonpath "$.gross_microcredits" == 50


POST {{pay_url}}/service/v1/payg-charges
Authorization: Bearer {{service_token}}
Content-Type: application/json
{
  "external_order_id": "ord_m02_commission_too_large",
  "api_key_hmac": "{{active_subscription_hmac}}",
  "gross_microcredits": 100,
  "commissions": [
    {"merchant_id":"{{provider_merchant_id}}","amount_microcredits":101}
  ],
  "order": {"type":"ai_call","title":"invalid commission"},
  "service_started_at": "2026-08-13T12:05:00Z",
  "service_completed_at": "2026-08-13T12:05:01Z"
}

HTTP 400
[Asserts]
jsonpath "$.error.code" == "commission_exceeds_gross"


# Duplicate beneficiary entries are rejected instead of creating two business
# Commission records for one Merchant.
POST {{pay_url}}/service/v1/payg-charges
Authorization: Bearer {{service_token}}
Content-Type: application/json
{
  "external_order_id": "ord_m02_duplicate_commission_merchant",
  "api_key_hmac": "{{active_subscription_hmac}}",
  "gross_microcredits": 100,
  "commissions": [
    {"merchant_id":"{{provider_merchant_id}}","amount_microcredits":40},
    {"merchant_id":"{{provider_merchant_id}}","amount_microcredits":40}
  ],
  "order": {"type":"ai_call","title":"duplicate merchant"},
  "service_started_at": "2026-08-13T12:05:00Z",
  "service_completed_at": "2026-08-13T12:05:01Z"
}

HTTP 400
[Asserts]
jsonpath "$.error.code" == "duplicate_commission_merchant"


# The fixed fee is taken only from the main Merchant. It may make that
# Merchant's settlement Account negative while the Provider keeps Commission.
POST {{pay_url}}/service/v1/payg-charges
Authorization: Bearer {{service_token}}
Content-Type: application/json
{
  "external_order_id": "ord_m02_negative_main_merchant_net",
  "api_key_hmac": "{{active_subscription_hmac}}",
  "gross_microcredits": 100,
  "commissions": [
    {"merchant_id":"{{provider_merchant_id}}","amount_microcredits":100}
  ],
  "order": {"type":"ai_call","title":"fee exceeds main share"},
  "service_started_at": "2026-08-13T12:05:00Z",
  "service_completed_at": "2026-08-13T12:05:01Z"
}

HTTP 201
[Asserts]
jsonpath "$.platform_fee_microcredits" == 2
jsonpath "$.main_merchant_net_microcredits" == -2
jsonpath "$.commissions[0].amount_microcredits" == 100


# One Charge may split Commission between multiple distinct Merchants. Each
# business Commission remains separate even if settlement Accounts may be
# shared in other fixtures.
POST {{pay_url}}/service/v1/payg-charges
Authorization: Bearer {{service_token}}
Content-Type: application/json
{
  "external_order_id": "ord_m02_multiple_commission_merchants",
  "api_key_hmac": "{{active_subscription_hmac}}",
  "gross_microcredits": 100,
  "commissions": [
    {"merchant_id":"{{provider_merchant_id}}","amount_microcredits":30},
    {"merchant_id":"{{provider_merchant_id_two}}","amount_microcredits":20}
  ],
  "order": {"type":"ai_call","title":"two provider beneficiaries"},
  "service_started_at": "2026-08-13T12:05:10Z",
  "service_completed_at": "2026-08-13T12:05:11Z"
}

HTTP 201
[Asserts]
jsonpath "$.platform_fee_microcredits" == 2
jsonpath "$.main_merchant_net_microcredits" == 48
jsonpath "$.commissions" count == 2
jsonpath "$.commissions[?(@.merchant_id == '{{provider_merchant_id}}')].amount_microcredits" contains 30
jsonpath "$.commissions[?(@.merchant_id == '{{provider_merchant_id_two}}')].amount_microcredits" contains 20


# ceil(1 * 200 / 10000) is 1, never zero. The fee is still taken only from
# the main Merchant, whose net for this Charge is exactly zero.
POST {{pay_url}}/service/v1/payg-charges
Authorization: Bearer {{service_token}}
Content-Type: application/json
{
  "external_order_id": "ord_m02_platform_fee_rounds_up",
  "api_key_hmac": "{{active_subscription_hmac}}",
  "gross_microcredits": 1,
  "commissions": [],
  "order": {"type":"ai_call","title":"fee rounding boundary"},
  "service_started_at": "2026-08-13T12:05:12Z",
  "service_completed_at": "2026-08-13T12:05:13Z"
}

HTTP 201
[Asserts]
jsonpath "$.gross_microcredits" == 1
jsonpath "$.platform_fee_microcredits" == 1
jsonpath "$.main_merchant_net_microcredits" == 0


# Every Commission amount is nonnegative.
POST {{pay_url}}/service/v1/payg-charges
Authorization: Bearer {{service_token}}
Content-Type: application/json
{
  "external_order_id": "ord_m02_negative_commission",
  "api_key_hmac": "{{active_subscription_hmac}}",
  "gross_microcredits": 100,
  "commissions": [
    {"merchant_id":"{{provider_merchant_id}}","amount_microcredits":-1}
  ],
  "order": {"type":"ai_call","title":"negative commission"},
  "service_started_at": "2026-08-13T12:05:14Z",
  "service_completed_at": "2026-08-13T12:05:15Z"
}

HTTP 400
[Asserts]
jsonpath "$.error.code" == "invalid_commission_amount"


POST {{pay_url}}/service/v1/payg-charges
Authorization: Bearer {{service_token}}
Content-Type: application/json
{
  "external_order_id": "ord_m02_unknown_commission_merchant",
  "api_key_hmac": "{{active_subscription_hmac}}",
  "gross_microcredits": 100,
  "commissions": [
    {"merchant_id":"merchant-does-not-exist","amount_microcredits":10}
  ],
  "order": {"type":"ai_call","title":"unknown beneficiary"},
  "service_started_at": "2026-08-13T12:05:16Z",
  "service_completed_at": "2026-08-13T12:05:17Z"
}

HTTP 400
[Asserts]
jsonpath "$.error.code" == "invalid_commission_merchant"


POST {{pay_url}}/service/v1/payg-charges
Authorization: Bearer {{service_token}}
Content-Type: application/json
{
  "external_order_id": "ord_m02_inactive_commission_merchant",
  "api_key_hmac": "{{active_subscription_hmac}}",
  "gross_microcredits": 100,
  "commissions": [
    {"merchant_id":"{{inactive_provider_merchant_id}}","amount_microcredits":10}
  ],
  "order": {"type":"ai_call","title":"inactive beneficiary"},
  "service_started_at": "2026-08-13T12:05:18Z",
  "service_completed_at": "2026-08-13T12:05:19Z"
}

HTTP 400
[Asserts]
jsonpath "$.error.code" == "commission_merchant_inactive"


# The Product's main Merchant must also be active before it can receive net
# revenue or bear the platform fee.
POST {{pay_url}}/service/v1/payg-charges
Authorization: Bearer {{service_token}}
Content-Type: application/json
{
  "external_order_id": "ord_m02_inactive_main_merchant",
  "api_key_hmac": "{{inactive_main_merchant_subscription_hmac}}",
  "gross_microcredits": 100,
  "commissions": [],
  "order": {"type":"ai_call","title":"inactive main Merchant"},
  "service_started_at": "2026-08-13T12:05:20Z",
  "service_completed_at": "2026-08-13T12:05:21Z"
}

HTTP 400
[Asserts]
jsonpath "$.error.code" == "main_merchant_inactive"


# Completion cannot precede service start.
POST {{pay_url}}/service/v1/payg-charges
Authorization: Bearer {{service_token}}
Content-Type: application/json
{
  "external_order_id": "ord_m02_invalid_time_order",
  "api_key_hmac": "{{active_subscription_hmac}}",
  "gross_microcredits": 10,
  "commissions": [],
  "order": {"type":"ai_call","title":"invalid timestamps"},
  "service_started_at": "2026-08-13T12:06:01Z",
  "service_completed_at": "2026-08-13T12:06:00Z"
}

HTTP 400
[Asserts]
jsonpath "$.error.code" == "invalid_service_time_range"


POST {{pay_url}}/service/v1/payg-charges
Authorization: Bearer {{service_token}}
Content-Type: application/json
{
  "external_order_id": "ord_m02_zero_gross",
  "api_key_hmac": "{{active_subscription_hmac}}",
  "gross_microcredits": 0,
  "commissions": [],
  "order": {"type":"ai_call","title":"zero"},
  "service_started_at": "2026-08-13T12:06:00Z",
  "service_completed_at": "2026-08-13T12:06:01Z"
}

HTTP 400
[Asserts]
jsonpath "$.error.code" == "invalid_gross_microcredits"


# An unknown HMAC cannot identify a payer and must not create any financial
# rows or alter any Account balance.
POST {{pay_url}}/service/v1/payg-charges
Authorization: Bearer {{service_token}}
Content-Type: application/json
{
  "external_order_id": "ord_m02_unknown_key",
  "api_key_hmac": "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
  "gross_microcredits": 10,
  "commissions": [],
  "order": {"type":"ai_call","title":"unknown key"},
  "service_started_at": "2026-08-13T12:06:00Z",
  "service_completed_at": "2026-08-13T12:06:01Z"
}

HTTP 404
[Asserts]
jsonpath "$.error.code" == "subscription_api_key_not_found"


# The contract fixture starts the payer at 200 and the receiving Accounts at
# zero. Five successful Charges total 351, so every resulting
# balance is exact rather than merely positive or negative.
GET {{pay_url}}/account/v1/accounts/{{payer_account_id}}/balance
Authorization: Bearer {{human_token_two}}

HTTP 200
[Asserts]
jsonpath "$.balance_microcredits" == -151


GET {{pay_url}}/account/v1/accounts/{{main_merchant_account_id}}/balance
Authorization: Bearer {{human_token}}

HTTP 200
[Asserts]
jsonpath "$.balance_microcredits" == 113


GET {{pay_url}}/account/v1/accounts/{{provider_settlement_account_id}}/balance
Authorization: Bearer {{provider_merchant_human_token}}

HTTP 200
[Asserts]
jsonpath "$.balance_microcredits" == 210


GET {{pay_url}}/account/v1/accounts/{{provider_settlement_account_id_two}}/balance
Authorization: Bearer {{provider_merchant_human_token_two}}

HTTP 200
[Asserts]
jsonpath "$.balance_microcredits" == 20


GET {{pay_url}}/account/v1/accounts/{{payer_account_id}}/charges
Authorization: Bearer {{human_token_two}}

HTTP 200
[Asserts]
jsonpath "$.data" count == 5
jsonpath "$.data[?(@.external_order_id == 'ord_m02_charge_0001')]" count == 1
jsonpath "$.data[?(@.external_order_id == 'ord_m02_revoked_key_late_charge')]" count == 1
jsonpath "$.data[?(@.external_order_id == 'ord_m02_negative_main_merchant_net')]" count == 1
jsonpath "$.data[?(@.external_order_id == 'ord_m02_multiple_commission_merchants')]" count == 1
jsonpath "$.data[?(@.external_order_id == 'ord_m02_platform_fee_rounds_up')]" count == 1
jsonpath "$.data[?(@.external_order_id == 'ord_m02_unknown_key')]" count == 0


GET {{pay_url}}/account/v1/accounts/{{payer_account_id}}/transactions?ledger_transaction_id={{ledger_transaction_id}}
Authorization: Bearer {{human_token_two}}

HTTP 200
[Asserts]
jsonpath "$.data" count == 1
jsonpath "$.data[0].id" == "{{ledger_transaction_id}}"
jsonpath "$.data[0].entries" count == 4
jsonpath "$.data[0].entries[?(@.account_id == '{{payer_account_id}}')].direction" contains "debit"
jsonpath "$.data[0].entries[?(@.account_id == '{{payer_account_id}}')].amount_microcredits" contains 100
jsonpath "$.data[0].entries[?(@.account_id == '{{provider_settlement_account_id}}')].direction" contains "credit"
jsonpath "$.data[0].entries[?(@.account_id == '{{provider_settlement_account_id}}')].amount_microcredits" contains 80
jsonpath "$.data[0].entries[?(@.account_id == '{{main_merchant_account_id}}')].amount_microcredits" contains 18
jsonpath "$.data[0].entries[?(@.account_id == '{{platform_fee_account_id}}')].amount_microcredits" contains 2


# The now-negative central balance denies the next service start.
POST {{pay_url}}/service/v1/subscription-credit-checks
Authorization: Bearer {{service_token}}
Content-Type: application/json
{"api_key_hmac":"{{active_subscription_hmac}}"}

HTTP 200
[Asserts]
jsonpath "$.status" == "denied"
jsonpath "$.available_microcredits" == 0


# Story test mode exposes a failpoint only to prove the Store transaction
# boundary. Failure after Commission creation must roll back Charge,
# Commission, ledger, and every balance mutation together.
POST {{pay_url}}/service/v1/payg-charges
Authorization: Bearer {{service_token}}
X-Gizway-Test-Failpoint: after_commission_before_ledger_commit
Content-Type: application/json
{
  "external_order_id": "ord_m02_injected_rollback",
  "api_key_hmac": "{{active_subscription_hmac}}",
  "gross_microcredits": 10,
  "commissions": [
    {"merchant_id":"{{provider_merchant_id}}","amount_microcredits":8}
  ],
  "order": {"type":"contract_probe","title":"transaction rollback"},
  "service_started_at": "2026-08-13T12:07:00Z",
  "service_completed_at": "2026-08-13T12:07:01Z"
}

HTTP 500
[Asserts]
jsonpath "$.error.code" == "injected_transaction_failure"


GET {{pay_url}}/service/v1/payg-charges/ord_m02_injected_rollback
Authorization: Bearer {{service_token}}

HTTP 404
[Asserts]
jsonpath "$.error.code" == "not_found"


GET {{pay_url}}/account/v1/accounts/{{payer_account_id}}/balance
Authorization: Bearer {{human_token_two}}

HTTP 200
[Asserts]
jsonpath "$.balance_microcredits" == -151
