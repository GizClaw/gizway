# M03 user-owned Provider Key commands. Reads belong to PowerSync.
# covers: gizway-user.yaml#createProviderKey gizway-user.yaml#putProviderKeyPrices gizway-user.yaml#disableProviderKey

POST {{way_url}}/user/v1/providers/{{provider_id}}/keys
Authorization: Bearer {{human_token}}
Content-Type: application/json
{
  "name": "M03 contributed key",
  "key": "{{provider_key_secret}}",
  "status": "active",
  "prices": [{"model_id":"{{seeded_model_id}}","metric":"input_tokens","unit_size":1000000,"microcredits_per_unit":1000}]
}

HTTP 201
[Captures]
provider_key_id: jsonpath "$.provider_key_id"
[Asserts]
jsonpath "$.provider_id" == "{{provider_id}}"
jsonpath "$.key" == "{{provider_key_secret}}"
jsonpath "$.merchant_id" exists


# Creation must explicitly opt into the only supported initial state.
POST {{way_url}}/user/v1/providers/{{provider_id}}/keys
Authorization: Bearer {{human_token}}
Content-Type: application/json
{
  "key": "{{provider_key_secret_two}}",
  "prices": [{"model_id":"{{seeded_model_id}}","metric":"input_tokens","unit_size":1000000,"microcredits_per_unit":1}]
}

HTTP 400
[Asserts]
jsonpath "$.error.code" == "invalid_request"


# Merchant ownership is server-derived and cannot be selected by the caller.
POST {{way_url}}/user/v1/providers/{{provider_id}}/keys
Authorization: Bearer {{human_token}}
Content-Type: application/json
{
  "key": "{{provider_key_secret_two}}",
  "merchant_id": "attacker-selected",
  "prices": [{"model_id":"{{seeded_model_id}}","metric":"input_tokens","unit_size":1000000,"microcredits_per_unit":1}]
}

HTTP 400
[Asserts]
jsonpath "$.error.code" == "invalid_request"


PUT {{way_url}}/user/v1/provider-keys/{{provider_key_id}}/prices
Authorization: Bearer {{human_token}}
Content-Type: application/json
{"prices":[{"model_id":"{{seeded_model_id}}","metric":"input_tokens","unit_size":1000000,"microcredits_per_unit":900}]}

HTTP 200
[Asserts]
jsonpath "$.prices[0].model_id" == "{{seeded_model_id}}"
jsonpath "$.prices[0].microcredits_per_unit" == 900


# Price updates cannot attach the key to a model owned by another provider.
PUT {{way_url}}/user/v1/provider-keys/{{provider_key_id}}/prices
Authorization: Bearer {{human_token}}
Content-Type: application/json
{"prices":[{"model_id":"model_unavailable","metric":"input_tokens","unit_size":1000000,"microcredits_per_unit":1}]}

HTTP 400
[Asserts]
jsonpath "$.error.code" == "invalid_request"


POST {{way_url}}/user/v1/provider-keys/{{provider_key_id}}/disable
Authorization: Bearer {{human_token}}

HTTP 200
[Asserts]
jsonpath "$.status" == "disabled"
