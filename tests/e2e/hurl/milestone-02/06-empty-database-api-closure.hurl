# Every identity and business/configuration resource in this scenario is
# created through Milestone 02 APIs. The test bootstrap injects only the payer's
# opening balance because top-up is intentionally outside this milestone.

POST {{way_url}}/admin/v1/providers
Authorization: Bearer {{cn_admin_token}}
Content-Type: application/json
{
  "name": "Empty API Provider",
  "kind": "openai",
  "base_url": "{{fake_url}}"
}

HTTP 201
[Captures]
provider_id: jsonpath "$.id"


POST {{way_url}}/admin/v1/models
Authorization: Bearer {{cn_admin_token}}
Content-Type: application/json
{
  "name": "empty-api-model",
  "provider_id": "{{provider_id}}",
  "provider_model": "fake-text-v1"
}

HTTP 201
[Captures]
model_id: jsonpath "$.id"


PUT {{way_url}}/admin/v1/models/{{model_id}}/prices
Authorization: Bearer {{cn_admin_token}}
Content-Type: application/json
{
  "prices": [
    {"metric":"input_token","unit_size":10,"price_microcredits":3},
    {"metric":"output_token","unit_size":10,"price_microcredits":3}
  ]
}

HTTP 200


POST {{way_url}}/admin/v1/providers/{{provider_id}}/api-keys
Authorization: Bearer {{cn_admin_token}}
Content-Type: application/json
{
  "name": "empty-api-key",
  "api_key": "cn-provider-secret",
  "weight": 100,
  "beneficiary_merchant_id": "{{provider_merchant_id}}",
  "prices": [
    {"model_id":"{{model_id}}","metric":"input_token","unit_size":10,"commission_microcredits":2},
    {"model_id":"{{model_id}}","metric":"output_token","unit_size":10,"commission_microcredits":1}
  ]
}

HTTP 201
[Asserts]
jsonpath "$.api_key" == "cn-provider-secret"


POST {{way_url}}/v1/chat/completions
Authorization: Bearer {{raw_subscription_key}}
Content-Type: application/json
{"model":"empty-api-model","messages":[{"role":"user","content":"empty database"}],"stream":false}

HTTP 200
[Asserts]
jsonpath "$.model" == "empty-api-model"


GET {{way_url}}/admin/v1/ai-orders
Authorization: Bearer {{cn_admin_token}}
[Options]
retry: 60
retry-interval: 100ms

HTTP 200
[Asserts]
jsonpath "$.data" count == 1
jsonpath "$.data[0].gross_microcredits" == 5
jsonpath "$.data[0].commission_microcredits" == 3


GET {{pay_url}}/account/v1/accounts/{{payer_account_id}}/charges
Authorization: Bearer {{human_token_two}}
[Options]
retry: 60
retry-interval: 100ms

HTTP 200
[Asserts]
jsonpath "$.data" count == 1
jsonpath "$.data[0].gross_microcredits" == 5


GET {{pay_url}}/account/v1/accounts/{{payer_account_id}}/balance
Authorization: Bearer {{human_token_two}}

HTTP 200
[Asserts]
jsonpath "$.balance_microcredits" == 999995
