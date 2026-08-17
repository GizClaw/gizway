# M04 client-generated IDs and content-sensitive idempotency.
# covers: account.yaml#createProductSubscription account.yaml#createSubscriptionKey
# covers: account.yaml#createAccountTopup gizway-user.yaml#createProviderKey

POST {{pay_url}}/account/v1/merchants/{{default_merchant_id}}/products
Authorization: Bearer {{human_token}}
Content-Type: application/json
{
  "name": "M04 idempotency PAYG",
  "billing_mode": "pay_as_you_go",
  "terms_version": "m04-v1"
}

HTTP 201
[Captures]
m04_product_id: jsonpath "$.id"


POST {{pay_url}}/account/v1/products/{{m04_product_id}}/subscriptions
Authorization: Bearer {{human_token_two}}
Content-Type: application/json
{
  "id": "sub_m04_hurl_fixed",
  "account_id": "{{account_id_two}}",
  "terms_version": "m04-v1"
}

HTTP 201
[Asserts]
jsonpath "$.id" == "sub_m04_hurl_fixed"


POST {{pay_url}}/account/v1/products/{{m04_product_id}}/subscriptions
Authorization: Bearer {{human_token_two}}
Content-Type: application/json
{
  "id": "sub_m04_hurl_fixed",
  "account_id": "{{account_id_two}}",
  "terms_version": "m04-v1"
}

HTTP 200
[Asserts]
jsonpath "$.id" == "sub_m04_hurl_fixed"


POST {{pay_url}}/account/v1/products/{{m04_product_id}}/subscriptions
Authorization: Bearer {{human_token_two}}
Content-Type: application/json
{
  "id": "sub_m04_hurl_fixed",
  "account_id": "{{account_id_two}}",
  "terms_version": "different"
}

HTTP 409
[Asserts]
jsonpath "$.error.code" == "resource_id_conflict"

POST {{pay_url}}/account/v1/subscriptions/{{seeded_subscription_id}}/keys
Authorization: Bearer {{human_token}}
Content-Type: application/json
{
  "id": "skey_m04_hurl_fixed",
  "name": "M04 CLI"
}

HTTP 201
[Captures]
m04_subscription_key: jsonpath "$.key"
[Asserts]
jsonpath "$.id" == "skey_m04_hurl_fixed"
jsonpath "$.name" == "M04 CLI"


POST {{pay_url}}/account/v1/subscriptions/{{seeded_subscription_id}}/keys
Authorization: Bearer {{human_token}}
Content-Type: application/json
{
  "id": "skey_m04_hurl_fixed",
  "name": "M04 CLI"
}

HTTP 200
[Asserts]
jsonpath "$.key" == "{{m04_subscription_key}}"


POST {{pay_url}}/account/v1/subscriptions/{{seeded_subscription_id}}/keys
Authorization: Bearer {{human_token}}
Content-Type: application/json
{
  "id": "skey_m04_hurl_fixed",
  "name": "Changed name"
}

HTTP 409
[Asserts]
jsonpath "$.error.code" == "resource_id_conflict"


POST {{pay_url}}/account/v1/accounts/{{account_id}}/topups
Authorization: Bearer {{human_token}}
Content-Type: application/json
{
  "id": "topup_m04_hurl_fixed",
  "channel": "fake",
  "external_reference": "m04-hurl-topup-fixed",
  "amount_microcredits": 1000
}

HTTP 201
[Captures]
m04_topup_transaction: jsonpath "$.ledger_transaction_id"


POST {{pay_url}}/account/v1/accounts/{{account_id}}/topups
Authorization: Bearer {{human_token}}
Content-Type: application/json
{
  "id": "topup_m04_hurl_fixed",
  "channel": "fake",
  "external_reference": "m04-hurl-topup-fixed",
  "amount_microcredits": 1000
}

HTTP 200
[Asserts]
jsonpath "$.ledger_transaction_id" == "{{m04_topup_transaction}}"


POST {{pay_url}}/account/v1/accounts/{{account_id}}/topups
Authorization: Bearer {{human_token}}
Content-Type: application/json
{
  "id": "topup_m04_hurl_fixed",
  "channel": "fake",
  "external_reference": "m04-hurl-topup-fixed",
  "amount_microcredits": 1001
}

HTTP 409
[Asserts]
jsonpath "$.error.code" == "resource_id_conflict"


POST {{pay_url}}/account/v1/accounts/{{account_id}}/topups
Authorization: Bearer {{human_token}}
Content-Type: application/json
{
  "id": "topup_m04_hurl_duplicate_reference",
  "channel": "fake",
  "external_reference": "m04-hurl-topup-fixed",
  "amount_microcredits": 1000
}

HTTP 409
[Asserts]
jsonpath "$.error.code" == "duplicate_external_reference"


POST {{way_url}}/user/v1/providers/{{provider_id}}/keys
Authorization: Bearer {{human_token}}
Content-Type: application/json
{
  "id": "pkey_m04_hurl_fixed",
  "name": "M04 contributed key",
  "key": "{{provider_key_secret_two}}",
  "status": "active",
  "prices": [
    {
      "model_id": "{{seeded_model_id}}",
      "metric": "input_tokens",
      "unit_size": 1000000,
      "microcredits_per_unit": 1000
    }
  ]
}

HTTP 201
[Asserts]
jsonpath "$.provider_key_id" == "pkey_m04_hurl_fixed"
jsonpath "$.name" == "M04 contributed key"


POST {{way_url}}/user/v1/providers/{{provider_id}}/keys
Authorization: Bearer {{human_token}}
Content-Type: application/json
{
  "id": "pkey_m04_hurl_fixed",
  "name": "M04 contributed key",
  "key": "{{provider_key_secret_two}}",
  "status": "active",
  "prices": [
    {
      "model_id": "{{seeded_model_id}}",
      "metric": "input_tokens",
      "unit_size": 1000000,
      "microcredits_per_unit": 1000
    }
  ]
}

HTTP 200
[Asserts]
jsonpath "$.provider_key_id" == "pkey_m04_hurl_fixed"


POST {{way_url}}/user/v1/providers/{{provider_id}}/keys
Authorization: Bearer {{human_token}}
Content-Type: application/json
{
  "id": "pkey_m04_hurl_fixed",
  "name": "Changed name",
  "key": "{{provider_key_secret_two}}",
  "status": "active",
  "prices": [
    {
      "model_id": "{{seeded_model_id}}",
      "metric": "input_tokens",
      "unit_size": 1000000,
      "microcredits_per_unit": 1000
    }
  ]
}

HTTP 409
[Asserts]
jsonpath "$.error.code" == "resource_id_conflict"
