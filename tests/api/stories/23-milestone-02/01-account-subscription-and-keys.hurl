# Milestone 02 — human Account API, PAYG Subscription, and credential lifecycle.
# covers: account.yaml#listAccounts
# covers: account.yaml#listServiceAccounts account.yaml#createServiceAccount account.yaml#revokeServiceAccount
# covers: account.yaml#listMerchants account.yaml#createMerchant account.yaml#getMerchant account.yaml#updateMerchant
# covers: account.yaml#listMerchantProducts account.yaml#createMerchantProduct account.yaml#getProduct account.yaml#updateProduct
# covers: account.yaml#createProductSubscription account.yaml#listSubscriptions account.yaml#getSubscription account.yaml#updateSubscription
# covers: account.yaml#listSubscriptionAPIKeys account.yaml#createSubscriptionAPIKey account.yaml#getSubscriptionAPIKey account.yaml#revokeSubscriptionAPIKey

# Simultaneous first requests for one previously unseen issuer+subject all
# succeed and converge on one User, Personal Account, and GIZ_CREDIT Ledger
# Account. The driver originates real concurrent requests to GizPay.
POST {{fake_url}}/drive-concurrent-account-login
Content-Type: application/json
{
  "pay_url": "{{pay_url}}",
  "human_token": "{{human_token}}",
  "count": 16
}

HTTP 200
[Captures]
main_merchant_account_id: jsonpath "$.account_ids[0]"
[Asserts]
jsonpath "$.requested" == 16
jsonpath "$.succeeded" == 16
jsonpath "$.failed" == 0
jsonpath "$.account_ids" count == 1
jsonpath "$.responses_with_one_personal_account" == 16
jsonpath "$.responses_with_one_giz_credit_ledger" == 16


# Replaying login for the same issuer+subject is idempotent. Mutable OIDC
# profile claims such as email cannot create a second User or Account.
GET {{pay_url}}/account/v1/accounts
Authorization: Bearer {{human_token_same_subject_changed_email}}

HTTP 200
[Asserts]
jsonpath "$.data" count == 1
jsonpath "$.data[0].id" == "{{main_merchant_account_id}}"


GET {{pay_url}}/account/v1/accounts
Authorization: Bearer {{human_token}}

HTTP 200
[Asserts]
jsonpath "$.data" count == 1
jsonpath "$.data[0].id" == "{{main_merchant_account_id}}"


# The payer is a different User with a different Personal Account. Merchant
# settlement and customer payment must never share the same fixture identity.
GET {{pay_url}}/account/v1/accounts
Authorization: Bearer {{human_token_two}}

HTTP 200
[Captures]
payer_account_id: jsonpath "$.data[0].id"
[Asserts]
jsonpath "$.data" count == 1
jsonpath "$.data[0].id" != "{{main_merchant_account_id}}"


# OpenAPI minLength applies after trimming; whitespace-only business identity
# fields must never reach PostgreSQL.
POST {{pay_url}}/account/v1/merchants
Authorization: Bearer {{human_token}}
Content-Type: application/json
{"settlement_account_id":"{{main_merchant_account_id}}","legal_name":"   ","public_name":"   "}

HTTP 400
[Asserts]
jsonpath "$.error.code" == "invalid_request"


POST {{pay_url}}/account/v1/merchants
Authorization: Bearer {{human_token}}
Content-Type: application/json
{
  "settlement_account_id": "{{main_merchant_account_id}}",
  "legal_name": "Milestone Merchant LLC",
  "public_name": "Milestone Merchant"
}

HTTP 201
[Captures]
merchant_id: jsonpath "$.id"
[Asserts]
jsonpath "$.id" exists
jsonpath "$.settlement_account_id" == "{{main_merchant_account_id}}"


# A settlement Account may own more than one Merchant.
POST {{pay_url}}/account/v1/merchants
Authorization: Bearer {{human_token}}
Content-Type: application/json
{
  "settlement_account_id": "{{main_merchant_account_id}}",
  "legal_name": "Second Milestone Merchant LLC",
  "public_name": "Second Milestone Merchant"
}

HTTP 201
[Captures]
second_merchant_id: jsonpath "$.id"
[Asserts]
jsonpath "$.id" != "{{merchant_id}}"
jsonpath "$.settlement_account_id" == "{{main_merchant_account_id}}"


GET {{pay_url}}/account/v1/merchants
Authorization: Bearer {{human_token}}

HTTP 200
[Asserts]
jsonpath "$.data[?(@.id == '{{merchant_id}}')]" count == 1
jsonpath "$.data[?(@.id == '{{second_merchant_id}}')]" count == 1


# The payer cannot read the seller's Account or Merchant resources.
GET {{pay_url}}/account/v1/accounts/{{main_merchant_account_id}}/balance
Authorization: Bearer {{human_token_two}}

HTTP 404
[Asserts]
jsonpath "$.error.code" == "not_found"


GET {{pay_url}}/account/v1/merchants/{{merchant_id}}
Authorization: Bearer {{human_token_two}}

HTTP 404
[Asserts]
jsonpath "$.error.code" == "not_found"


GET {{pay_url}}/account/v1/merchants/{{merchant_id}}
Authorization: Bearer {{human_token}}

HTTP 200
[Asserts]
jsonpath "$.public_name" == "Milestone Merchant"


PATCH {{pay_url}}/account/v1/merchants/{{merchant_id}}
Authorization: Bearer {{human_token}}
Content-Type: application/json
{"public_name":"Milestone Merchant Updated"}

HTTP 200
[Asserts]
jsonpath "$.public_name" == "Milestone Merchant Updated"


POST {{pay_url}}/account/v1/merchants/{{merchant_id}}/products
Authorization: Bearer {{human_token}}
Content-Type: application/json
{"name":"   ","billing_mode":"pay_as_you_go","terms_version":"   "}

HTTP 400
[Asserts]
jsonpath "$.error.code" == "invalid_request"


POST {{pay_url}}/account/v1/merchants/{{merchant_id}}/products
Authorization: Bearer {{human_token}}
Content-Type: application/json
{
  "name": "Milestone PAYG",
  "billing_mode": "pay_as_you_go",
  "terms_version": "2026-08-13"
}

HTTP 201
[Captures]
product_id: jsonpath "$.id"
[Asserts]
jsonpath "$.billing_mode" == "pay_as_you_go"


GET {{pay_url}}/account/v1/merchants/{{merchant_id}}/products
Authorization: Bearer {{human_token}}

HTTP 200
[Asserts]
jsonpath "$.data[?(@.id == '{{product_id}}')]" count == 1


GET {{pay_url}}/account/v1/products/{{product_id}}
Authorization: Bearer {{human_token}}

HTTP 200
[Asserts]
jsonpath "$.billing_mode" == "pay_as_you_go"


PATCH {{pay_url}}/account/v1/products/{{product_id}}
Authorization: Bearer {{human_token}}
Content-Type: application/json
{"name":"Milestone PAYG Updated"}

HTTP 200
[Asserts]
jsonpath "$.name" == "Milestone PAYG Updated"


# One-time payment is explicitly outside Milestone 02.
POST {{pay_url}}/account/v1/merchants/{{merchant_id}}/products
Authorization: Bearer {{human_token}}
Content-Type: application/json
{
  "name": "Forbidden one-time",
  "billing_mode": "one_time",
  "terms_version": "2026-08-13"
}

HTTP 400
[Asserts]
jsonpath "$.error.code" == "unsupported_billing_mode"


# Subscription authorization must capture the Product's current terms, not an
# arbitrary or stale version supplied by the caller.
POST {{pay_url}}/account/v1/products/{{product_id}}/subscriptions
Authorization: Bearer {{human_token_two}}
Content-Type: application/json
{"account_id":"{{payer_account_id}}","terms_version":"   "}

HTTP 400
[Asserts]
jsonpath "$.error.code" == "invalid_request"


POST {{pay_url}}/account/v1/products/{{product_id}}/subscriptions
Authorization: Bearer {{human_token_two}}
Content-Type: application/json
{
  "account_id": "{{payer_account_id}}",
  "terms_version": "stale-terms"
}

HTTP 409
[Asserts]
jsonpath "$.error.code" == "terms_version_mismatch"


POST {{pay_url}}/account/v1/products/{{product_id}}/subscriptions
Authorization: Bearer {{human_token_two}}
Content-Type: application/json
{
  "account_id": "{{payer_account_id}}",
  "terms_version": "2026-08-13"
}

HTTP 201
[Captures]
subscription_id: jsonpath "$.id"
[Asserts]
jsonpath "$.product_id" == "{{product_id}}"
jsonpath "$.status" == "active"


GET {{pay_url}}/account/v1/subscriptions
Authorization: Bearer {{human_token_two}}

HTTP 200
[Asserts]
jsonpath "$.data[?(@.id == '{{subscription_id}}')]" count == 1


GET {{pay_url}}/account/v1/subscriptions/{{subscription_id}}
Authorization: Bearer {{human_token_two}}

HTTP 200
[Asserts]
jsonpath "$.product_id" == "{{product_id}}"


# The Merchant owner is not the payer and cannot read the payer's Account or
# Subscription merely because the Subscription references its Product.
GET {{pay_url}}/account/v1/accounts/{{payer_account_id}}/balance
Authorization: Bearer {{human_token}}

HTTP 404
[Asserts]
jsonpath "$.error.code" == "not_found"


GET {{pay_url}}/account/v1/subscriptions/{{subscription_id}}
Authorization: Bearer {{human_token}}

HTTP 404
[Asserts]
jsonpath "$.error.code" == "not_found"


PATCH {{pay_url}}/account/v1/subscriptions/{{subscription_id}}
Authorization: Bearer {{human_token_two}}
Content-Type: application/json
{"status":"paused"}

HTTP 200
[Asserts]
jsonpath "$.status" == "paused"


PATCH {{pay_url}}/account/v1/subscriptions/{{subscription_id}}
Authorization: Bearer {{human_token_two}}
Content-Type: application/json
{"status":"active"}

HTTP 200
[Asserts]
jsonpath "$.status" == "active"


# The same Account may create another Subscription to the same Product for
# project isolation; the two credentials must remain distinct.
POST {{pay_url}}/account/v1/products/{{product_id}}/subscriptions
Authorization: Bearer {{human_token_two}}
Content-Type: application/json
{
  "account_id": "{{payer_account_id}}",
  "terms_version": "2026-08-13"
}

HTTP 201
[Captures]
second_subscription_id: jsonpath "$.id"
[Asserts]
jsonpath "$.id" != "{{subscription_id}}"


POST {{pay_url}}/account/v1/subscriptions/{{subscription_id}}/api-keys
Authorization: Bearer {{human_token_two}}
Content-Type: application/json
{"name":"project-a"}

HTTP 201
[Captures]
api_key_id: jsonpath "$.id"
raw_api_key: jsonpath "$.api_key"
[Asserts]
jsonpath "$.api_key" exists
jsonpath "$.status" == "active"
body not contains "key_hmac"
body not contains "encrypted_key"


POST {{pay_url}}/account/v1/subscriptions/{{subscription_id}}/api-keys
Authorization: Bearer {{human_token_two}}
Content-Type: application/json
{"name":"project-b"}

HTTP 201
[Captures]
second_api_key_id: jsonpath "$.id"
[Asserts]
jsonpath "$.id" != "{{api_key_id}}"


# GizPay can decrypt and reveal the raw key to the owning human API client.
GET {{pay_url}}/account/v1/subscriptions/{{subscription_id}}/api-keys/{{api_key_id}}
Authorization: Bearer {{human_token_two}}

HTTP 200
[Asserts]
jsonpath "$.api_key" == "{{raw_api_key}}"
jsonpath "$.status" == "active"
body not contains "key_hmac"
body not contains "encrypted_key"


GET {{pay_url}}/account/v1/subscriptions/{{subscription_id}}/api-keys
Authorization: Bearer {{human_token_two}}

HTTP 200
[Asserts]
jsonpath "$.data" count == 2
body not contains "key_hmac"
body not contains "encrypted_key"


# The Key belongs to the path Subscription. An owner cannot substitute another
# owned Subscription ID to read or revoke it.
GET {{pay_url}}/account/v1/subscriptions/{{second_subscription_id}}/api-keys/{{api_key_id}}
Authorization: Bearer {{human_token_two}}

HTTP 404
[Asserts]
jsonpath "$.error.code" == "not_found"
body not contains "{{raw_api_key}}"


POST {{pay_url}}/account/v1/subscriptions/{{second_subscription_id}}/api-keys/{{api_key_id}}/revoke
Authorization: Bearer {{human_token_two}}

HTTP 404
[Asserts]
jsonpath "$.error.code" == "not_found"


# The rejected cross-Subscription revoke did not change the Key.
GET {{pay_url}}/account/v1/subscriptions/{{subscription_id}}/api-keys/{{api_key_id}}
Authorization: Bearer {{human_token_two}}

HTTP 200
[Asserts]
jsonpath "$.status" == "active"
jsonpath "$.api_key" == "{{raw_api_key}}"


# A different human cannot reveal the recoverable credential.
GET {{pay_url}}/account/v1/subscriptions/{{subscription_id}}/api-keys/{{api_key_id}}
Authorization: Bearer {{human_token}}

HTTP 404
[Asserts]
jsonpath "$.error.code" == "not_found"
body not contains "{{raw_api_key}}"


POST {{pay_url}}/account/v1/subscriptions/{{subscription_id}}/api-keys/{{api_key_id}}/revoke
Authorization: Bearer {{human_token_two}}

HTTP 200
[Asserts]
jsonpath "$.status" == "revoked"
jsonpath "$.revoked_at" exists


# Revocation is irreversible and a second transition is an explicit conflict.
POST {{pay_url}}/account/v1/subscriptions/{{subscription_id}}/api-keys/{{api_key_id}}/revoke
Authorization: Bearer {{human_token_two}}

HTTP 409
[Asserts]
jsonpath "$.error.code" == "api_key_already_revoked"


POST {{pay_url}}/account/v1/service-accounts
Authorization: Bearer {{human_token}}
Content-Type: application/json
{
  "name": "temporary-gateway",
  "roles": ["subscription_credit_reader", "subscription_charger"]
}

HTTP 201
[Captures]
service_account_id: jsonpath "$.id"
[Asserts]
jsonpath "$.status" == "active"


# One User may own multiple independently revocable machine principals.
POST {{pay_url}}/account/v1/service-accounts
Authorization: Bearer {{human_token}}
Content-Type: application/json
{
  "name": "backup-gateway",
  "roles": ["subscription_credit_reader", "subscription_charger"]
}

HTTP 201
[Captures]
second_service_account_id: jsonpath "$.id"
[Asserts]
jsonpath "$.id" != "{{service_account_id}}"
jsonpath "$.status" == "active"


DELETE {{pay_url}}/account/v1/service-accounts/{{service_account_id}}
Authorization: Bearer {{human_token}}

HTTP 204


GET {{pay_url}}/account/v1/service-accounts
Authorization: Bearer {{human_token}}

HTTP 200
[Asserts]
jsonpath "$.data[?(@.id == '{{service_account_id}}')]" count == 1
jsonpath "$.data[?(@.id == '{{service_account_id}}')].status" contains "revoked"
jsonpath "$.data[?(@.id == '{{service_account_id}}')].revoked_at" exists
jsonpath "$.data[?(@.id == '{{second_service_account_id}}')]" count == 1
jsonpath "$.data[?(@.id == '{{second_service_account_id}}')].status" contains "active"


GET {{pay_url}}/account/v1/service-accounts
Authorization: Bearer {{human_token_two}}

HTTP 200
[Asserts]
jsonpath "$.data[?(@.id == '{{service_account_id}}')]" count == 0
jsonpath "$.data[?(@.id == '{{second_service_account_id}}')]" count == 0
