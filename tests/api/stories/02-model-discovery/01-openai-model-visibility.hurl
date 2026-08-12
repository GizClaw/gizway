# Story 02 — Discover currently usable canonical models through OpenAI API.
#
# Product behavior:
# - The public model ID is Gizway's canonical slug, never an upstream endpoint
#   ID, provider model name, or credential reference.
# - A model is visible only when model, provider, endpoint, variant, and a
#   current price are all active and routable.
# - Invalid and suspended credentials are rejected before catalog disclosure.
#
# Fixture: story-base contains one active/priced and one disabled model.
# Isolation: this file receives a new PostgreSQL schema and server process.
# protocol-case: openai.models canonical-visibility bearer-header
# protocol covers: OpenAI GET /v1/models

GET {{way_url}}/v1/models
Authorization: Bearer {{gateway_api_key}}

HTTP 200
[Asserts]
jsonpath "$.object" == "list"
jsonpath "$.data" count == 1
jsonpath "$.data[0].id" == "story-text"
jsonpath "$.data[0].object" == "model"
jsonpath "$.data[0].owned_by" == "gizway"
jsonpath "$.data[0].provider_endpoint_id" not exists
jsonpath "$.data[0].provider_model_name" not exists
jsonpath "$.data[0].credential_ref" not exists


GET {{way_url}}/v1/models
Authorization: Bearer invalid-model-key

HTTP 401
[Asserts]
jsonpath "$.error.code" == "invalid_api_key"


GET {{way_url}}/v1/models
Authorization: Bearer {{suspended_user_key}}

HTTP 401
[Asserts]
jsonpath "$.error.code" == "invalid_api_key"
