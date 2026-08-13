# Milestone 02 — GizWay Admin is the external manager for embedded Bifrost.
# covers: gizway-admin.yaml#createProvider gizway-admin.yaml#getProvider gizway-admin.yaml#updateProvider gizway-admin.yaml#listProviders
# covers: gizway-admin.yaml#listProviderAPIKeys gizway-admin.yaml#createProviderAPIKey gizway-admin.yaml#getProviderAPIKey gizway-admin.yaml#updateProviderAPIKey gizway-admin.yaml#disableProviderAPIKey
# covers: gizway-admin.yaml#getProviderAPIKeyBilling gizway-admin.yaml#putProviderAPIKeyBilling
# covers: gizway-admin.yaml#getProviderAPIKeyPrices gizway-admin.yaml#putProviderAPIKeyPrices
# covers: gizway-admin.yaml#createModel gizway-admin.yaml#getModel gizway-admin.yaml#updateModel gizway-admin.yaml#listAdminModels
# covers: gizway-admin.yaml#getModelPrices gizway-admin.yaml#putModelPrices

POST {{way_url}}/admin/v1/providers
Authorization: Bearer {{admin_token}}
Content-Type: application/json
{"name":"   ","kind":"openai","base_url":"{{fake_url}}"}

HTTP 400
[Asserts]
jsonpath "$.error.code" == "invalid_request"


POST {{way_url}}/admin/v1/providers
Authorization: Bearer {{admin_token}}
Content-Type: application/json
{"name":"embedded-fake","kind":"openai","base_url":"{{fake_url}}"}

HTTP 201
[Captures]
provider_id: jsonpath "$.id"
[Asserts]
jsonpath "$.status" == "active"


GET {{way_url}}/admin/v1/providers
Authorization: Bearer {{admin_token}}

HTTP 200
[Asserts]
jsonpath "$.data[?(@.id == '{{provider_id}}')]" count == 1


GET {{way_url}}/admin/v1/providers/{{provider_id}}
Authorization: Bearer {{admin_token}}

HTTP 200
[Asserts]
jsonpath "$.name" == "embedded-fake"


PATCH {{way_url}}/admin/v1/providers/{{provider_id}}
Authorization: Bearer {{admin_token}}
Content-Type: application/json
{"name":"embedded-fake-updated"}

HTTP 200
[Asserts]
jsonpath "$.name" == "embedded-fake-updated"


POST {{way_url}}/admin/v1/models
Authorization: Bearer {{admin_token}}
Content-Type: application/json
{"name":"   ","provider_id":"{{provider_id}}","provider_model":"   "}

HTTP 400
[Asserts]
jsonpath "$.error.code" == "invalid_request"


POST {{way_url}}/admin/v1/models
Authorization: Bearer {{admin_token}}
Content-Type: application/json
{
  "name": "admin-story-text",
  "provider_id": "{{provider_id}}",
  "provider_model": "fake-text-001"
}

HTTP 201
[Captures]
model_id: jsonpath "$.id"
[Asserts]
jsonpath "$.provider_id" == "{{provider_id}}"


GET {{way_url}}/admin/v1/models
Authorization: Bearer {{admin_token}}

HTTP 200
[Asserts]
jsonpath "$.data[?(@.id == '{{model_id}}')]" count == 1


GET {{way_url}}/admin/v1/models/{{model_id}}
Authorization: Bearer {{admin_token}}

HTTP 200
[Asserts]
jsonpath "$.provider_model" == "fake-text-001"


PATCH {{way_url}}/admin/v1/models/{{model_id}}
Authorization: Bearer {{admin_token}}
Content-Type: application/json
{"name":"story-text-updated"}

HTTP 200
[Asserts]
jsonpath "$.name" == "story-text-updated"


PUT {{way_url}}/admin/v1/models/{{model_id}}/prices
Authorization: Bearer {{admin_token}}
Content-Type: application/json
{"prices":[{"metric":"input_token","unit_size":0,"price_microcredits":0}]}

HTTP 400
[Asserts]
jsonpath "$.error.code" == "invalid_request"


PUT {{way_url}}/admin/v1/models/{{model_id}}/prices
Authorization: Bearer {{admin_token}}
Content-Type: application/json
{
  "prices": [
    {"metric":"input_token","unit_size":1000000,"price_microcredits":2000},
    {"metric":"output_token","unit_size":1000000,"price_microcredits":4000}
  ]
}

HTTP 200
[Asserts]
jsonpath "$.prices" count == 2


GET {{way_url}}/admin/v1/models/{{model_id}}/prices
Authorization: Bearer {{admin_token}}

HTTP 200
[Asserts]
jsonpath "$.prices" count == 2


# This is one aggregate operation: Bifrost gets the callable Secret while the
# GizWay schema gets billing and Commission mappings before the Key is active.
POST {{way_url}}/admin/v1/providers/{{provider_id}}/api-keys
Authorization: Bearer {{admin_token}}
Content-Type: application/json
{
  "name": "missing-required-status",
  "api_key": "not-persisted",
  "weight": 100,
  "beneficiary_merchant_id": "{{provider_merchant_id}}",
  "prices": []
}

HTTP 400


POST {{way_url}}/admin/v1/providers/{{provider_id}}/api-keys
Authorization: Bearer {{admin_token}}
Content-Type: application/json
{
  "name": "provider-key-a",
  "api_key": "{{provider_key_secret}}",
  "weight": 100,
  "status": "active",
  "beneficiary_merchant_id": "{{provider_merchant_id}}",
  "prices": [
    {
      "model_id": "{{model_id}}",
      "metric": "input_token",
      "unit_size": 1000000,
      "commission_microcredits": 1000
    }
  ]
}

HTTP 201
[Captures]
bifrost_key_id: jsonpath "$.bifrost_key_id"
[Asserts]
jsonpath "$.status" == "active"
jsonpath "$.provider_id" == "{{provider_id}}"
jsonpath "$.api_key" == "{{provider_key_secret}}"
jsonpath "$.beneficiary_merchant_id" == "{{provider_merchant_id}}"
jsonpath "$.prices[0].model_id" == "{{model_id}}"
jsonpath "$.prices[0].commission_microcredits" == 1000


# Authorized administrators can read the dynamic upstream credential.
GET {{way_url}}/admin/v1/provider-api-keys/{{bifrost_key_id}}
Authorization: Bearer {{admin_token}}

HTTP 200
[Asserts]
jsonpath "$.api_key" == "{{provider_key_secret}}"
jsonpath "$.bifrost_key_id" == "{{bifrost_key_id}}"


# Replacing the upstream credential updates the Bifrost Config Store. A later
# authorized read returns the complete replacement, never the stale value.
PATCH {{way_url}}/admin/v1/provider-api-keys/{{bifrost_key_id}}
Authorization: Bearer {{admin_token}}
Content-Type: application/json
{
  "api_key": "{{provider_key_secret_replacement}}",
  "weight": 80
}

HTTP 200
[Asserts]
jsonpath "$.weight" == 80


GET {{way_url}}/admin/v1/provider-api-keys/{{bifrost_key_id}}
Authorization: Bearer {{admin_token}}

HTTP 200
[Asserts]
jsonpath "$.api_key" == "{{provider_key_secret_replacement}}"
jsonpath "$.api_key" != "{{provider_key_secret}}"


GET {{way_url}}/admin/v1/provider-api-keys/{{bifrost_key_id}}/billing
Authorization: Bearer {{admin_token}}

HTTP 200
[Asserts]
jsonpath "$.beneficiary_merchant_id" == "{{provider_merchant_id}}"


# Merchant binding is mutable independently from the Bifrost Secret. The next
# selected-Key result must resolve the new binding.
PUT {{way_url}}/admin/v1/provider-api-keys/{{bifrost_key_id}}/billing
Authorization: Bearer {{admin_token}}
Content-Type: application/json
{"beneficiary_merchant_id":"{{provider_merchant_id_two}}","status":"active"}

HTTP 200
[Asserts]
jsonpath "$.beneficiary_merchant_id" == "{{provider_merchant_id_two}}"


GET {{way_url}}/admin/v1/provider-api-keys/{{bifrost_key_id}}/prices
Authorization: Bearer {{admin_token}}

HTTP 200
[Asserts]
jsonpath "$.prices[0].model_id" == "{{model_id}}"
jsonpath "$.prices[0].commission_microcredits" == 1000


PUT {{way_url}}/admin/v1/provider-api-keys/{{bifrost_key_id}}/prices
Authorization: Bearer {{admin_token}}
Content-Type: application/json
{
  "prices": [
    {
      "model_id": "{{model_id}}",
      "metric": "input_token",
      "unit_size": 1000000,
      "commission_microcredits": 1200
    }
  ]
}

HTTP 200
[Asserts]
jsonpath "$.prices[0].commission_microcredits" == 1200


# With exactly one active Key, the embedded Bifrost result must expose that
# stable Key ID and GizWay must resolve its Merchant and Commission mapping.
POST {{way_url}}/v1/chat/completions
Authorization: Bearer {{raw_subscription_key}}
Content-Type: application/json
{"model":"story-text-updated","messages":[{"role":"user","content":"select key a"}],"stream":false}

HTTP 200


GET {{way_url}}/admin/v1/bifrost-logs
Authorization: Bearer {{admin_token}}

HTTP 200
[Asserts]
jsonpath "$.data" count == 1
jsonpath "$.data[0].selected_key_id" == "{{bifrost_key_id}}"
jsonpath "$.data[0].beneficiary_merchant_id" == "{{provider_merchant_id_two}}"
jsonpath "$.data[0].commission_microcredits" == 1


# One Provider may contain multiple independently managed credentials.
POST {{way_url}}/admin/v1/providers/{{provider_id}}/api-keys
Authorization: Bearer {{admin_token}}
Content-Type: application/json
{
  "name": "provider-key-b",
  "api_key": "{{provider_key_secret_two}}",
  "weight": 100,
  "status": "active",
  "beneficiary_merchant_id": "{{provider_merchant_id_two}}",
  "prices": [
    {
      "model_id": "{{model_id}}",
      "metric": "input_token",
      "unit_size": 1000000,
      "commission_microcredits": 2400
    }
  ]
}

HTTP 201
[Captures]
bifrost_key_id_two: jsonpath "$.bifrost_key_id"
[Asserts]
jsonpath "$.bifrost_key_id" != "{{bifrost_key_id}}"
jsonpath "$.status" == "active"


GET {{way_url}}/admin/v1/provider-api-keys/{{bifrost_key_id_two}}
Authorization: Bearer {{admin_token}}

HTTP 200
[Asserts]
jsonpath "$.api_key" == "{{provider_key_secret_two}}"
jsonpath "$.beneficiary_merchant_id" == "{{provider_merchant_id_two}}"


GET {{way_url}}/admin/v1/providers/{{provider_id}}/api-keys
Authorization: Bearer {{admin_token}}

HTTP 200
[Asserts]
jsonpath "$.data" count == 2
jsonpath "$.data[?(@.bifrost_key_id == '{{bifrost_key_id}}')]" count == 1
jsonpath "$.data[?(@.bifrost_key_id == '{{bifrost_key_id_two}}')]" count == 1


# Missing payment mapping must never leave a callable active Bifrost Key.
POST {{way_url}}/admin/v1/providers/{{provider_id}}/api-keys
Authorization: Bearer {{admin_token}}
Content-Type: application/json
{
  "name": "incomplete-key",
  "api_key": "incomplete-secret",
  "weight": 100,
  "status": "active",
  "prices": []
}

HTTP 400
[Asserts]
jsonpath "$.error.code" == "incomplete_provider_key_billing"


GET {{way_url}}/admin/v1/providers/{{provider_id}}/api-keys
Authorization: Bearer {{admin_token}}

HTTP 200
[Asserts]
jsonpath "$.data[?(@.name == 'incomplete-key')].status" not contains "active"


POST {{way_url}}/admin/v1/provider-api-keys/{{bifrost_key_id}}/disable
Authorization: Bearer {{admin_token}}

HTTP 200
[Asserts]
jsonpath "$.status" == "disabled"


# Once Key A is disabled, the next execution can only select Key B. The
# selected key's own Merchant and Commission rate are used.
POST {{way_url}}/v1/chat/completions
Authorization: Bearer {{raw_subscription_key}}
Content-Type: application/json
{"model":"story-text-updated","messages":[{"role":"user","content":"select key b"}],"stream":false}

HTTP 200


GET {{way_url}}/admin/v1/bifrost-logs
Authorization: Bearer {{admin_token}}

HTTP 200
[Asserts]
jsonpath "$.data" count == 2
jsonpath "$.data[?(@.selected_key_id == '{{bifrost_key_id_two}}')]" count == 1
jsonpath "$.data[?(@.selected_key_id == '{{bifrost_key_id_two}}')].beneficiary_merchant_id" contains "{{provider_merchant_id_two}}"
jsonpath "$.data[?(@.selected_key_id == '{{bifrost_key_id_two}}')].commission_microcredits" contains 1


# A different Provider and Model are deliberately callable, but they are not a
# fallback target for the original Model.
POST {{way_url}}/admin/v1/providers
Authorization: Bearer {{admin_token}}
Content-Type: application/json
{"name":"alternate-fake","kind":"openai","base_url":"{{fake_url}}"}

HTTP 201
[Captures]
alternate_provider_id: jsonpath "$.id"


POST {{way_url}}/admin/v1/models
Authorization: Bearer {{admin_token}}
Content-Type: application/json
{
  "name": "alternate-story-text",
  "provider_id": "{{alternate_provider_id}}",
  "provider_model": "fake-text-002"
}

HTTP 201
[Captures]
alternate_model_id: jsonpath "$.id"


POST {{way_url}}/admin/v1/providers/{{alternate_provider_id}}/api-keys
Authorization: Bearer {{admin_token}}
Content-Type: application/json
{
  "name": "alternate-key",
  "api_key": "{{provider_key_secret_three}}",
  "weight": 100,
  "status": "active",
  "beneficiary_merchant_id": "{{provider_merchant_id}}",
  "prices": []
}

HTTP 201
[Captures]
alternate_bifrost_key_id: jsonpath "$.bifrost_key_id"


POST {{way_url}}/admin/v1/provider-api-keys/{{bifrost_key_id_two}}/disable
Authorization: Bearer {{admin_token}}

HTTP 200
[Asserts]
jsonpath "$.status" == "disabled"


POST {{way_url}}/v1/chat/completions
Authorization: Bearer {{raw_subscription_key}}
Content-Type: application/json
{"model":"story-text-updated","messages":[{"role":"user","content":"must not fallback"}],"stream":false}

HTTP 503
[Asserts]
jsonpath "$.error.code" == "no_active_provider_key"


GET {{way_url}}/admin/v1/bifrost-logs
Authorization: Bearer {{admin_token}}

HTTP 200
[Asserts]
jsonpath "$.data[?(@.selected_key_id == '{{alternate_bifrost_key_id}}')]" count == 0
