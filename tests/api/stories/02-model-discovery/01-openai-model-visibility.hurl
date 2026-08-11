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
# covers: listAccountModels

GET {{base_url}}/v1/models
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


# The first-party Account catalog is intentionally richer than compatible
# discovery: it exposes executable capabilities, variant limits, baseline and
# effective customer prices and discount, while still redacting provider
# endpoint/model/credential routing.
GET {{base_url}}/account/v1/accounts/21000000-0000-4000-8000-000000000001/models
Authorization: Bearer {{active_user_one_key}}

HTTP 200
[Asserts]
jsonpath "$.data" count == 1
jsonpath "$.data[0].id" == "story-text"
jsonpath "$.data[0].capabilities" contains "chat"
jsonpath "$.data[0].variants" count == 2
jsonpath "$.data[0].variants[0].capabilities.chat" == true
jsonpath "$.data[0].variants[0].context_window" == 8192
jsonpath "$.data[0].variants[0].max_output_tokens" == 2048
jsonpath "$.data[0].variants[0].prices[1].metric" == "cached_input_token"
jsonpath "$.data[0].variants[0].prices[1].base_price_microcredits" == 1000
jsonpath "$.data[0].variants[0].prices[1].effective_price_microcredits" == 900
jsonpath "$.data[0].variants[0].prices[1].discount_bps" == 1000
jsonpath "$.data[0].variants[0].provider_endpoint_id" not exists
jsonpath "$.data[0].variants[0].provider_model_name" not exists
jsonpath "$.data[0].variants[0].credential_ref" not exists


# An authenticated user still cannot request another user's account catalog.
GET {{base_url}}/account/v1/accounts/21000000-0000-4000-8000-000000000001/models
Authorization: Bearer {{active_user_two_key}}

HTTP 404


GET {{base_url}}/v1/models
Authorization: Bearer invalid-model-key

HTTP 401
[Asserts]
jsonpath "$.error.code" == "unauthorized"


GET {{base_url}}/v1/models
Authorization: Bearer {{suspended_user_key}}

HTTP 401
[Asserts]
jsonpath "$.error.code" == "unauthorized"
