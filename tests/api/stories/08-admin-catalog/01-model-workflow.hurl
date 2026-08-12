# Story 08 — Publish a canonical model, variant, and immutable price.
#
# This is the migrated scaffold slice. It proves real database writes and
# read-after-write behavior; later Story 08 files add providers, credential
# rotation, overlapping-price rejection, audit, and catalog visibility.
#
# Fixture: story-base
# Isolation: this file receives a new PostgreSQL schema and server process.
# covers: gizway-admin.yaml#listAdminModels gizway-admin.yaml#createModel gizway-admin.yaml#updateModel gizway-admin.yaml#createModelVariant
# covers: gizway-admin.yaml#listModelVariants gizway-admin.yaml#updateModelVariant gizway-admin.yaml#listModelVariantPrices
# covers: gizway-admin.yaml#publishModelVariantPrice

GET {{way_url}}/admin/v1/models
Authorization: Bearer {{admin_api_key}}

HTTP 200
[Asserts]
jsonpath "$.data" count == 1


POST {{way_url}}/admin/v1/models
Authorization: Bearer {{admin_api_key}}
Idempotency-Key: story-08-create-model
X-Request-ID: story-08-create-model-request
Content-Type: application/json
{
  "slug": "story-created-model",
  "name": "Story Created Model",
  "modality": ["text"],
  "metadata": {"source": "hurl"}
}

HTTP 201
[Captures]
model_id: jsonpath "$.id"
[Asserts]
jsonpath "$.slug" == "story-created-model"
jsonpath "$.status" == "active"


# Catalog commands persist credential/operation/key, payload fingerprint and
# the exact result. A network retry therefore returns the same resource and
# status without repeating either the mutation or its audit event.
POST {{way_url}}/admin/v1/models
Authorization: Bearer {{admin_api_key}}
Idempotency-Key: story-08-create-model
Content-Type: application/json
{
  "slug": "story-created-model",
  "name": "Story Created Model",
  "modality": ["text"],
  "metadata": {"source": "hurl"}
}

HTTP 201
[Asserts]
jsonpath "$.id" == "{{model_id}}"


# Reusing that scoped key for a different command payload is a conflict and
# cannot mutate the already-created model.
POST {{way_url}}/admin/v1/models
Authorization: Bearer {{admin_api_key}}
Idempotency-Key: story-08-create-model
Content-Type: application/json
{
  "slug": "must-not-exist",
  "name": "Changed Payload",
  "modality": ["text"],
  "metadata": {}
}

HTTP 409
[Asserts]
jsonpath "$.error.code" == "idempotency_conflict"


PATCH {{way_url}}/admin/v1/models/{{model_id}}
Authorization: Bearer {{admin_api_key}}
Idempotency-Key: story-08-update-model
Content-Type: application/json
{
  "name": "Story Created Model Updated",
  "status": "deprecated"
}

HTTP 200
[Asserts]
jsonpath "$.name" == "Story Created Model Updated"
jsonpath "$.status" == "deprecated"


# PATCH response and catalog mutation are one atomic command; replay cannot
# append another model.updated audit row.
PATCH {{way_url}}/admin/v1/models/{{model_id}}
Authorization: Bearer {{admin_api_key}}
Idempotency-Key: story-08-update-model
Content-Type: application/json
{
  "name": "Story Created Model Updated",
  "status": "deprecated"
}

HTTP 200
[Asserts]
jsonpath "$.id" == "{{model_id}}"


PATCH {{way_url}}/admin/v1/models/{{model_id}}
Authorization: Bearer {{admin_api_key}}
Idempotency-Key: story-08-update-model
Content-Type: application/json
{
  "name": "Conflicting Retry",
  "status": "active"
}

HTTP 409
[Asserts]
jsonpath "$.error.code" == "idempotency_conflict"


POST {{way_url}}/admin/v1/models/{{model_id}}/variants
Authorization: Bearer {{admin_api_key}}
Idempotency-Key: story-08-create-variant
Content-Type: application/json
{
  "provider_endpoint_id": "71000000-0000-4000-8000-000000000001",
  "provider_model_name": "story-created-model-v1",
  "variant_slug": "fake-story-created",
  "capabilities": {"chat": true},
  "context_window": 16384,
  "max_output_tokens": 4096
}

HTTP 201
[Captures]
variant_id: jsonpath "$.id"
[Asserts]
jsonpath "$.model_id" == "{{model_id}}"
jsonpath "$.variant_slug" == "fake-story-created"


POST {{way_url}}/admin/v1/model_variants/{{variant_id}}/prices
Authorization: Bearer {{admin_api_key}}
Idempotency-Key: story-08-create-price
Content-Type: application/json
{
  "metric": "input_token",
  "unit_size": 1000,
  "upstream_cost_microcredits": 1000,
  "base_customer_price_microcredits": 2000,
  "customer_price_microcredits": 1800,
  "discount_bps": 1000,
  "valid_from": "2026-08-11T00:00:00Z",
  "valid_until": null
}

HTTP 201
[Asserts]
jsonpath "$.base_customer_price_microcredits" == 2000
jsonpath "$.customer_price_microcredits" == 1800
jsonpath "$.discount_bps" == 1000


GET {{way_url}}/admin/v1/model_variants/{{variant_id}}/prices
Authorization: Bearer {{admin_api_key}}

HTTP 200
[Asserts]
jsonpath "$.data" count == 1
jsonpath "$.data[0].metric" == "input_token"


# Price versions are append-only and cannot overlap for the same metric. A
# second open-ended interval at the same activation instant is ambiguous.
POST {{way_url}}/admin/v1/model_variants/{{variant_id}}/prices
Authorization: Bearer {{admin_api_key}}
Idempotency-Key: story-08-overlapping-price
Content-Type: application/json
{
  "metric": "input_token",
  "unit_size": 1000,
  "upstream_cost_microcredits": 1100,
  "base_customer_price_microcredits": 2200,
  "customer_price_microcredits": 1980,
  "discount_bps": 1000,
  "valid_from": "2026-08-11T00:00:00Z",
  "valid_until": null
}

HTTP 409
[Asserts]
jsonpath "$.error.code" == "conflict"


# A price and active status do not authorize every protocol. This variant only
# declares chat capability, so embedding discovery fails before Bifrost or the
# external provider is called.
POST {{way_url}}/v1/embeddings
Authorization: Bearer {{gateway_api_key}}
Idempotency-Key: story-08-capability-denied
Content-Type: application/json
{
  "model": "story-created-model",
  "input": "capabilities are authorization"
}

HTTP 404
[Asserts]
jsonpath "$.error.code" == "model_not_found"


# Variant state is persisted independently from the canonical model. Disabling
# it removes it from routing without deleting historical configuration.
PATCH {{way_url}}/admin/v1/model_variants/{{variant_id}}
Authorization: Bearer {{admin_api_key}}
Idempotency-Key: story-08-disable-variant
Content-Type: application/json
{
  "provider_model_name": "story-created-model-v1",
  "capabilities": {"chat": true},
  "context_window": 16384,
  "max_output_tokens": 4096,
  "status": "disabled"
}

HTTP 200
[Asserts]
jsonpath "$.id" == "{{variant_id}}"
jsonpath "$.status" == "disabled"


GET {{way_url}}/admin/v1/models/{{model_id}}/variants
Authorization: Bearer {{admin_api_key}}

HTTP 200
[Asserts]
jsonpath "$.data" count == 1
jsonpath "$.data[0].id" == "{{variant_id}}"
jsonpath "$.data[0].status" == "disabled"
