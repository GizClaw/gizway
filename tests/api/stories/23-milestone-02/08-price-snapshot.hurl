# Milestone 02 — an AI request snapshots customer and Provider-Key prices
# before calling Bifrost. Later Admin edits affect only later requests.
# protocol-case: milestone02.openai.chat.price-snapshot request-start

PUT {{way_url}}/admin/v1/models/{{seeded_model_id}}/prices
Authorization: Bearer {{admin_token}}
Content-Type: application/json
{
  "prices": [
    {"metric":"input_token","unit_size":10,"price_microcredits":3},
    {"metric":"output_token","unit_size":10,"price_microcredits":3}
  ]
}

HTTP 200


PUT {{way_url}}/admin/v1/provider-api-keys/{{seeded_bifrost_key_id}}/prices
Authorization: Bearer {{admin_token}}
Content-Type: application/json
{
  "prices": [
    {"model_id":"{{seeded_model_id}}","metric":"input_token","unit_size":10,"commission_microcredits":2},
    {"model_id":"{{seeded_model_id}}","metric":"output_token","unit_size":10,"commission_microcredits":1}
  ]
}

HTTP 200


# The Fake Provider starts a real customer request and returns only after the
# request has reached the upstream boundary. At that point GizWay must already
# hold immutable price snapshots.
POST {{fake_url}}/drive-held-chat
Content-Type: application/json
{
  "api_url":"{{way_url}}",
  "api_key":"{{raw_subscription_key}}",
  "model":"story-text"
}

HTTP 202
[Captures]
held_chat_job_id: jsonpath "$.job_id"
[Asserts]
jsonpath "$.provider_reached" == true


# These new prices are intentionally extreme. They must not affect the held
# request, whose normalized usage will be 10 input and 5 output tokens.
PUT {{way_url}}/admin/v1/models/{{seeded_model_id}}/prices
Authorization: Bearer {{admin_token}}
Content-Type: application/json
{
  "prices": [
    {"metric":"input_token","unit_size":1,"price_microcredits":999},
    {"metric":"output_token","unit_size":1,"price_microcredits":999}
  ]
}

HTTP 200


PUT {{way_url}}/admin/v1/provider-api-keys/{{seeded_bifrost_key_id}}/prices
Authorization: Bearer {{admin_token}}
Content-Type: application/json
{
  "prices": [
    {"model_id":"{{seeded_model_id}}","metric":"input_token","unit_size":1,"commission_microcredits":888},
    {"model_id":"{{seeded_model_id}}","metric":"output_token","unit_size":1,"commission_microcredits":888}
  ]
}

HTTP 200


POST {{fake_url}}/release-held-chat/{{held_chat_job_id}}

HTTP 204


GET {{fake_url}}/held-chat/{{held_chat_job_id}}
[Options]
retry: 40
retry-interval: 50ms

HTTP 200
[Asserts]
jsonpath "$.done" == true
jsonpath "$.status" == 200


GET {{way_url}}/admin/v1/ai-orders
Authorization: Bearer {{admin_token}}
[Options]
retry: 40
retry-interval: 50ms

HTTP 200
[Asserts]
jsonpath "$.data" count == 1
# Old snapshot: ceil(10*3/10) + ceil(5*3/10) = 5.
jsonpath "$.data[0].gross_microcredits" == 5
# Old Provider snapshot: ceil(10*2/10) + ceil(5*1/10) = 3.
jsonpath "$.data[0].commission_microcredits" == 3
