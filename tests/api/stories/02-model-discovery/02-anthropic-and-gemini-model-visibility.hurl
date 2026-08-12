# Story 02 — Anthropic and Gemini clients discover the same canonical catalog.
#
# Durable product requirements:
# - The same Gizway Gateway credential is accepted in each SDK's native header:
#   Anthropic `x-api-key` and Gemini `x-goog-api-key`.
# - Every ecosystem receives its own public list shape, but all IDs resolve to
#   the same Gizway canonical `story-text` model used by inference stories.
# - Disabled/unpriced models, private provider model names, endpoint IDs and
#   credential references are never disclosed.
# - Catalog authorization runs before the database projection; a credential
#   from another class cannot use these compatible discovery routes.
#
# Fixture/isolation: fresh PostgreSQL story-base with one active/priced model and
# one disabled model. No provider call is needed for catalog discovery.
# protocol-case: anthropic.models canonical-visibility native-header
# protocol-case: gemini.models canonical-visibility native-header
# protocol covers: Anthropic GET /v1/models
# protocol covers: Gemini GET /v1beta/models

GET {{way_url}}/v1/models
x-api-key: {{gateway_api_key}}
anthropic-version: 2023-06-01

HTTP 200
[Asserts]
jsonpath "$.data" count == 1
jsonpath "$.data[0].id" == "story-text"
jsonpath "$.data[0].type" == "model"
jsonpath "$.data[0].display_name" == "story-text"
jsonpath "$.has_more" == false
jsonpath "$.first_id" == "story-text"
jsonpath "$.last_id" == "story-text"
jsonpath "$.data[0].provider_model_name" not exists
jsonpath "$.data[0].credential_ref" not exists


GET {{way_url}}/v1beta/models
x-goog-api-key: {{gateway_api_key}}

HTTP 200
[Asserts]
jsonpath "$.models" count == 1
jsonpath "$.models[0].name" == "models/story-text"
jsonpath "$.models[0].baseModelId" == "story-text"
jsonpath "$.models[0].version" == "story-text"
jsonpath "$.models[0].supportedGenerationMethods" count == 2
jsonpath "$.models[0].provider_endpoint_id" not exists
jsonpath "$.models[0].credential_ref" not exists


# A user-session credential is not a Gateway credential and cannot enumerate
# compatible inference models even when placed in a native Gemini header.
GET {{way_url}}/v1beta/models
x-goog-api-key: {{active_user_one_key}}

HTTP 401
[Asserts]
jsonpath "$.error.code" == "invalid_api_key"
