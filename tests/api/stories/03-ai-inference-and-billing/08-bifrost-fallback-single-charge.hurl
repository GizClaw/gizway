# Story 03 — Bifrost fallback crosses endpoints but creates one charge.
#
# Durable product requirements:
# - The ordered candidate set comes entirely from database catalog records.
# - Each candidate retains its own endpoint and credential. The primary fake
#   returns 500 twice (Bifrost MaxRetries=1); Bifrost then invokes the second
#   endpoint with its different credential and succeeds.
# - Every supported non-Realtime protocol gives its complete candidate set to
#   Bifrost. All HTTP attempts belong to one Gizway request/reservation, and
#   only terminal usage from the actual winner is priced and posted once.
# - Public model identity remains canonical while Admin attribution records the
#   exact fallback variant. Provider URLs, credentials and routing metadata are
#   never returned publicly.
#
# Fixture/isolation: fresh story-base PostgreSQL plus two independent fake provider
# processes supplied as fake_url and catalog_fake_url by the runner.
# protocol-case: cross-provider-endpoint fallback one-request one-charge actual-winner
# protocol covers: OpenAI fallback POST /v1/chat/completions
# protocol covers: OpenAI fallback POST /v1/embeddings
# protocol covers: OpenAI fallback POST /v1/audio/speech
# protocol covers: OpenAI fallback POST /v1/audio/transcriptions
# protocol covers: OpenAI fallback POST /v1/images/generations
# covers: createProvider createProviderEndpoint createModel createModelVariant
# covers: publishModelVariantPrice getAccountBalance listGatewayUsage listGatewayRequests

POST {{base_url}}/admin/v1/providers
Authorization: Bearer {{admin_api_key}}
Idempotency-Key: story-03-fallback-provider
Content-Type: application/json
{
  "slug": "story-cross-endpoint-provider",
  "name": "Story Cross Endpoint Provider"
}

HTTP 201
[Captures]
provider_id: jsonpath "$.id"


POST {{base_url}}/admin/v1/providers/{{provider_id}}/endpoints
Authorization: Bearer {{admin_api_key}}
Idempotency-Key: story-03-primary-endpoint
Content-Type: application/json
{
  "name": "Primary failing endpoint",
  "base_url": "{{fake_url}}",
  "credential": "story-provider-key",
  "priority": 1,
  "weight": 100
}

HTTP 201
[Captures]
primary_endpoint_id: jsonpath "$.id"


POST {{base_url}}/admin/v1/providers/{{provider_id}}/endpoints
Authorization: Bearer {{admin_api_key}}
Idempotency-Key: story-03-fallback-endpoint
Content-Type: application/json
{
  "name": "Secondary healthy endpoint",
  "base_url": "{{catalog_fake_url}}",
  "credential": "story-catalog-provider-key",
  "priority": 2,
  "weight": 100
}

HTTP 201
[Captures]
fallback_endpoint_id: jsonpath "$.id"


POST {{base_url}}/admin/v1/models
Authorization: Bearer {{admin_api_key}}
Idempotency-Key: story-03-fallback-model
Content-Type: application/json
{
  "slug": "story-cross-endpoint",
  "name": "Story Cross Endpoint Model",
  "modality": ["text"],
  "metadata": {"test_contract": "cross-endpoint-fallback"}
}

HTTP 201
[Captures]
model_id: jsonpath "$.id"


POST {{base_url}}/admin/v1/models/{{model_id}}/variants
Authorization: Bearer {{admin_api_key}}
Idempotency-Key: story-03-primary-variant
Content-Type: application/json
{
  "provider_endpoint_id": "{{primary_endpoint_id}}",
  "provider_model_name": "fake-fallback-primary",
  "variant_slug": "primary",
  "capabilities": {
    "chat": true,
    "embeddings": true,
    "audio_speech": true,
    "audio_transcriptions": true,
    "image_generation": true
  },
  "context_window": 8192,
  "max_output_tokens": 2048
}

HTTP 201
[Captures]
primary_variant_id: jsonpath "$.id"


POST {{base_url}}/admin/v1/models/{{model_id}}/variants
Authorization: Bearer {{admin_api_key}}
Idempotency-Key: story-03-fallback-variant
Content-Type: application/json
{
  "provider_endpoint_id": "{{fallback_endpoint_id}}",
  "provider_model_name": "fake-fallback-secondary",
  "variant_slug": "secondary",
  "capabilities": {
    "chat": true,
    "embeddings": true,
    "audio_speech": true,
    "audio_transcriptions": true,
    "image_generation": true
  },
  "context_window": 8192,
  "max_output_tokens": 2048
}

HTTP 201
[Captures]
fallback_variant_id: jsonpath "$.id"


# Both candidates receive immutable, active prices. The winner's snapshot is
# the only one copied into gateway_request_charges.
POST {{base_url}}/admin/v1/model_variants/{{primary_variant_id}}/prices
Authorization: Bearer {{admin_api_key}}
Idempotency-Key: story-03-primary-input-price
Content-Type: application/json
{
  "metric": "input_token", "unit_size": 1000,
  "upstream_cost_microcredits": 1000,
  "base_customer_price_microcredits": 2000,
  "customer_price_microcredits": 1800,
  "discount_bps": 1000,
  "valid_from": "2026-08-09T00:00:00Z", "valid_until": null
}

HTTP 201


POST {{base_url}}/admin/v1/model_variants/{{primary_variant_id}}/prices
Authorization: Bearer {{admin_api_key}}
Idempotency-Key: story-03-primary-output-price
Content-Type: application/json
{
  "metric": "output_token", "unit_size": 1000,
  "upstream_cost_microcredits": 2000,
  "base_customer_price_microcredits": 4000,
  "customer_price_microcredits": 3600,
  "discount_bps": 1000,
  "valid_from": "2026-08-09T00:00:00Z", "valid_until": null
}

HTTP 201


POST {{base_url}}/admin/v1/model_variants/{{fallback_variant_id}}/prices
Authorization: Bearer {{admin_api_key}}
Idempotency-Key: story-03-fallback-input-price
Content-Type: application/json
{
  "metric": "input_token", "unit_size": 1000,
  "upstream_cost_microcredits": 1000,
  "base_customer_price_microcredits": 2000,
  "customer_price_microcredits": 1800,
  "discount_bps": 1000,
  "valid_from": "2026-08-09T00:00:00Z", "valid_until": null
}

HTTP 201


POST {{base_url}}/admin/v1/model_variants/{{fallback_variant_id}}/prices
Authorization: Bearer {{admin_api_key}}
Idempotency-Key: story-03-fallback-output-price
Content-Type: application/json
{
  "metric": "output_token", "unit_size": 1000,
  "upstream_cost_microcredits": 2000,
  "base_customer_price_microcredits": 4000,
  "customer_price_microcredits": 3600,
  "discount_bps": 1000,
  "valid_from": "2026-08-09T00:00:00Z", "valid_until": null
}

HTTP 201


# Embedding fallback requires an explicit cached-input price even when this
# deterministic response reports zero cached tokens. Missing pricing must
# remove a candidate rather than silently charge a different metric.
POST {{base_url}}/admin/v1/model_variants/{{primary_variant_id}}/prices
Authorization: Bearer {{admin_api_key}}
Idempotency-Key: story-03-primary-cached-input-price
Content-Type: application/json
{
  "metric": "cached_input_token", "unit_size": 1000,
  "upstream_cost_microcredits": 500,
  "base_customer_price_microcredits": 1000,
  "customer_price_microcredits": 900,
  "discount_bps": 1000,
  "valid_from": "2026-08-09T00:00:00Z", "valid_until": null
}

HTTP 201


POST {{base_url}}/admin/v1/model_variants/{{fallback_variant_id}}/prices
Authorization: Bearer {{admin_api_key}}
Idempotency-Key: story-03-fallback-cached-input-price
Content-Type: application/json
{
  "metric": "cached_input_token", "unit_size": 1000,
  "upstream_cost_microcredits": 500,
  "base_customer_price_microcredits": 1000,
  "customer_price_microcredits": 900,
  "discount_bps": 1000,
  "valid_from": "2026-08-09T00:00:00Z", "valid_until": null
}

HTTP 201


# Audio and image protocols require an independently measurable request price
# plus their protocol-specific duration/image prices on every candidate.
POST {{base_url}}/admin/v1/model_variants/{{primary_variant_id}}/prices
Authorization: Bearer {{admin_api_key}}
Idempotency-Key: story-03-primary-request-price
Content-Type: application/json
{
  "metric": "request", "unit_size": 1,
  "upstream_cost_microcredits": 5,
  "base_customer_price_microcredits": 10,
  "customer_price_microcredits": 9,
  "discount_bps": 1000,
  "valid_from": "2026-08-09T00:00:00Z", "valid_until": null
}

HTTP 201


POST {{base_url}}/admin/v1/model_variants/{{fallback_variant_id}}/prices
Authorization: Bearer {{admin_api_key}}
Idempotency-Key: story-03-fallback-request-price
Content-Type: application/json
{
  "metric": "request", "unit_size": 1,
  "upstream_cost_microcredits": 5,
  "base_customer_price_microcredits": 10,
  "customer_price_microcredits": 9,
  "discount_bps": 1000,
  "valid_from": "2026-08-09T00:00:00Z", "valid_until": null
}

HTTP 201


POST {{base_url}}/admin/v1/model_variants/{{primary_variant_id}}/prices
Authorization: Bearer {{admin_api_key}}
Idempotency-Key: story-03-primary-audio-price
Content-Type: application/json
{
  "metric": "audio_second", "unit_size": 1,
  "upstream_cost_microcredits": 10,
  "base_customer_price_microcredits": 20,
  "customer_price_microcredits": 18,
  "discount_bps": 1000,
  "valid_from": "2026-08-09T00:00:00Z", "valid_until": null
}

HTTP 201


POST {{base_url}}/admin/v1/model_variants/{{fallback_variant_id}}/prices
Authorization: Bearer {{admin_api_key}}
Idempotency-Key: story-03-fallback-audio-price
Content-Type: application/json
{
  "metric": "audio_second", "unit_size": 1,
  "upstream_cost_microcredits": 10,
  "base_customer_price_microcredits": 20,
  "customer_price_microcredits": 18,
  "discount_bps": 1000,
  "valid_from": "2026-08-09T00:00:00Z", "valid_until": null
}

HTTP 201


POST {{base_url}}/admin/v1/model_variants/{{primary_variant_id}}/prices
Authorization: Bearer {{admin_api_key}}
Idempotency-Key: story-03-primary-image-price
Content-Type: application/json
{
  "metric": "image", "unit_size": 1,
  "upstream_cost_microcredits": 50,
  "base_customer_price_microcredits": 100,
  "customer_price_microcredits": 90,
  "discount_bps": 1000,
  "valid_from": "2026-08-09T00:00:00Z", "valid_until": null
}

HTTP 201


POST {{base_url}}/admin/v1/model_variants/{{fallback_variant_id}}/prices
Authorization: Bearer {{admin_api_key}}
Idempotency-Key: story-03-fallback-image-price
Content-Type: application/json
{
  "metric": "image", "unit_size": 1,
  "upstream_cost_microcredits": 50,
  "base_customer_price_microcredits": 100,
  "customer_price_microcredits": 90,
  "discount_bps": 1000,
  "valid_from": "2026-08-09T00:00:00Z", "valid_until": null
}

HTTP 201


POST {{base_url}}/v1/chat/completions
Authorization: Bearer {{gateway_api_key}}
Idempotency-Key: story-03-bifrost-fallback
Content-Type: application/json
{
  "model": "story-cross-endpoint",
  "messages": [{"role": "user", "content": "fallback-required"}],
  "stream": false
}

HTTP 200
[Asserts]
jsonpath "$.id" == "fake-chat-001"
jsonpath "$.model" == "story-cross-endpoint"
jsonpath "$.choices[0].message.content" == "deterministic story response"
jsonpath "$.usage.prompt_tokens" == 10
jsonpath "$.usage.completion_tokens" == 5
jsonpath "$.extra_fields" not exists


POST {{base_url}}/v1/embeddings
Authorization: Bearer {{gateway_api_key}}
Idempotency-Key: story-03-embedding-fallback
Content-Type: application/json
{
  "model": "story-cross-endpoint",
  "input": "fallback-required"
}

HTTP 200
[Asserts]
jsonpath "$.model" == "story-cross-endpoint"
jsonpath "$.data" count == 1
jsonpath "$.usage.prompt_tokens" == 8


POST {{base_url}}/v1/audio/speech
Authorization: Bearer {{gateway_api_key}}
Idempotency-Key: story-03-speech-fallback
Content-Type: application/json
{
  "model": "story-cross-endpoint",
  "input": "fallback-required",
  "voice": "alloy",
  "response_format": "mp3"
}

HTTP 200
[Asserts]
body contains "GIZWAY-DETERMINISTIC-MP3"


POST {{base_url}}/v1/audio/transcriptions
Authorization: Bearer {{gateway_api_key}}
Idempotency-Key: story-03-transcription-fallback
[Multipart]
model: story-cross-endpoint
response_format: verbose_json
file: file,fixtures/story-audio.txt; text/plain

HTTP 200
[Asserts]
jsonpath "$.text" == "deterministic transcript"
jsonpath "$.usage.seconds" == 2.4


POST {{base_url}}/v1/images/generations
Authorization: Bearer {{gateway_api_key}}
Idempotency-Key: story-03-image-fallback
Content-Type: application/json
{
  "model": "story-cross-endpoint",
  "prompt": "fallback-required",
  "n": 1,
  "response_format": "b64_json"
}

HTTP 200
[Asserts]
jsonpath "$.model" == "story-cross-endpoint"
jsonpath "$.data" count == 1


GET {{fake_url}}/events

HTTP 200
[Asserts]
jsonpath "$.chat_calls" == 2
jsonpath "$.embedding_calls" == 2
jsonpath "$.speech_calls" == 2
jsonpath "$.transcription_calls" == 2
jsonpath "$.image_calls" == 2


GET {{catalog_fake_url}}/events

HTTP 200
[Asserts]
jsonpath "$.chat_calls" == 1
jsonpath "$.embedding_calls" == 1
jsonpath "$.speech_calls" == 1
jsonpath "$.transcription_calls" == 1
jsonpath "$.image_calls" == 1


GET {{base_url}}/account/v1/accounts/21000000-0000-4000-8000-000000000001/balance
Authorization: Bearer {{active_user_one_key}}

HTTP 200
[Asserts]
jsonpath "$.microcredits" == 99999750


GET {{base_url}}/account/v1/accounts/21000000-0000-4000-8000-000000000001/usage?from=2026-08-10T00%3A00%3A00Z&to=2027-08-11T00%3A00%3A00Z
Authorization: Bearer {{active_user_one_key}}

HTTP 200
[Asserts]
jsonpath "$.data" count == 5


GET {{base_url}}/admin/v1/gateway_requests
Authorization: Bearer {{admin_api_key}}

HTTP 200
[Asserts]
jsonpath "$.data" count == 5
jsonpath "$.data[0].model_variant_id" == "{{fallback_variant_id}}"
jsonpath "$.data[0].status" == "succeeded"
jsonpath "$.data[1].model_variant_id" == "{{fallback_variant_id}}"
jsonpath "$.data[1].status" == "succeeded"
jsonpath "$.data[2].model_variant_id" == "{{fallback_variant_id}}"
jsonpath "$.data[2].status" == "succeeded"
jsonpath "$.data[3].model_variant_id" == "{{fallback_variant_id}}"
jsonpath "$.data[3].status" == "succeeded"
jsonpath "$.data[4].model_variant_id" == "{{fallback_variant_id}}"
jsonpath "$.data[4].status" == "succeeded"
