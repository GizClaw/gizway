# Story 03 — Gemini embeddings and explicit provider limitations.
#
# Durable product requirements:
# - Gemini embedContent/batchEmbedContents use native request/response shapes,
#   canonical Gizway model IDs and the same measured embedding price engine.
# - A Bifrost type existing in source does not imply that this configured
#   OpenAI-compatible supplier supports every lifecycle API. Image edits,
#   Anthropic batches/files and Gemini files/batch generation therefore return
#   a stable explicit unsupported_operation instead of silent rerouting or 404.
# - Unsupported commands still require the correct Gateway credential and an
#   Idempotency-Key, but create no reservation, provider call or charge.
#
# protocol-case: gemini.embed-content dimensions-and-usage
# protocol-case: gemini.batch-embed-contents input-variants-and-usage
# protocol-case: openai.images.edit explicit-provider-limitation
# protocol-case: anthropic.batches-files explicit-provider-limitation
# protocol-case: gemini.files-batches explicit-provider-limitation
# protocol covers: Gemini POST /v1beta/models/{model}:embedContent
# protocol covers: Gemini POST /v1beta/models/{model}:batchEmbedContents

POST {{base_url}}/v1beta/models/story-text:embedContent
x-goog-api-key: {{gateway_api_key}}
Idempotency-Key: story-03-gemini-embed-one
Content-Type: application/json
{
  "content": {"parts": [{"text": "embed one Gemini input"}]},
  "outputDimensionality": 2
}

HTTP 200
[Asserts]
jsonpath "$.embedding.values" count == 2


POST {{base_url}}/v1beta/models/story-text:batchEmbedContents
x-goog-api-key: {{gateway_api_key}}
Idempotency-Key: story-03-gemini-embed-batch
Content-Type: application/json
{
  "requests": [
    {"content": {"parts": [{"text": "first"}]}, "outputDimensionality": 2},
    {"content": {"parts": [{"text": "second"}]}, "outputDimensionality": 2}
  ]
}

HTTP 200
[Asserts]
jsonpath "$.embeddings" count == 2
jsonpath "$.embeddings[0].values" count == 2
jsonpath "$.embeddings[1].values" count == 2


# This supplier exposes image generation but not multipart image editing.
POST {{base_url}}/v1/images/edits
Authorization: Bearer {{gateway_api_key}}
Idempotency-Key: story-03-unsupported-image-edit
Content-Type: application/json
{}

HTTP 501
[Asserts]
jsonpath "$.error.code" == "unsupported_operation"


POST {{base_url}}/v1/messages/batches
x-api-key: {{gateway_api_key}}
Idempotency-Key: story-03-unsupported-anthropic-batch
Content-Type: application/json
{}

HTTP 501
[Asserts]
jsonpath "$.error.code" == "unsupported_operation"


GET {{base_url}}/v1/files
x-api-key: {{gateway_api_key}}

HTTP 501
[Asserts]
jsonpath "$.error.code" == "unsupported_operation"


POST {{base_url}}/v1beta/models/story-text:batchGenerateContent
x-goog-api-key: {{gateway_api_key}}
Idempotency-Key: story-03-unsupported-gemini-batch
Content-Type: application/json
{}

HTTP 501
[Asserts]
jsonpath "$.error.code" == "unsupported_operation"


GET {{base_url}}/v1beta/files
x-goog-api-key: {{gateway_api_key}}

HTTP 501
[Asserts]
jsonpath "$.error.code" == "unsupported_operation"


GET {{fake_url}}/events

HTTP 200
[Asserts]
jsonpath "$.embedding_calls" == 2


GET {{base_url}}/account/v1/accounts/21000000-0000-4000-8000-000000000001/usage?from=2026-08-10T00%3A00%3A00Z&to=2027-08-11T00%3A00%3A00Z
Authorization: Bearer {{active_user_one_key}}

HTTP 200
[Asserts]
jsonpath "$.data" count == 2
jsonpath "$.data[0].charged_microcredits" == 24
jsonpath "$.data[1].charged_microcredits" == 24
