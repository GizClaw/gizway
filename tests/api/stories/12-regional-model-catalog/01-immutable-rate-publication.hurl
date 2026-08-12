# Story 12 — A region publishes pricing without copying its Catalog or secrets.
#
# Node and region come from mTLS. The body contains only opaque model identities
# and prices; Provider endpoint and credential fields are forbidden. Publication
# source ID plus revision and canonical content hash make ambiguous-response
# recovery explicit. The returned `id` is GizPay's distinct financial snapshot
# identity and is what subsequent Usage references.
# covers: internal-gizpay.yaml#publishRatePublication internal-gizpay.yaml#getRatePublication

POST {{pay_url}}/internal/v1/rate-publications
Content-Type: application/json
{
  "source_publication_id": "ratepub_story_hurl_12",
  "revision": 12,
  "effective_at": "2026-08-11T12:00:00Z",
  "prices": [
    {
      "model_variant_id": "variant_story_hurl_12",
      "public_model": "regional-story-model",
      "metric": "request",
      "unit_size": 1,
      "base_price_microcredits": 10,
      "discount_bps": 1000,
      "customer_price_microcredits": 9
    }
  ]
}

HTTP 201
[Asserts]
jsonpath "$.id" exists
jsonpath "$.source_publication_id" == "ratepub_story_hurl_12"
jsonpath "$.revision" == 12
jsonpath "$.status" == "active"
jsonpath "$.content_sha256" matches "^[0-9a-f]{64}$"
body not contains "credential"
body not contains "endpoint"


# Exact replay is acknowledged without creating a second snapshot.
POST {{pay_url}}/internal/v1/rate-publications
Content-Type: application/json
{
  "source_publication_id": "ratepub_story_hurl_12",
  "revision": 12,
  "effective_at": "2026-08-11T12:00:00Z",
  "prices": [
    {
      "model_variant_id": "variant_story_hurl_12",
      "public_model": "regional-story-model",
      "metric": "request",
      "unit_size": 1,
      "base_price_microcredits": 10,
      "discount_bps": 1000,
      "customer_price_microcredits": 9
    }
  ]
}

HTTP 200
[Asserts]
jsonpath "$.source_publication_id" == "ratepub_story_hurl_12"


# Same ID with different content cannot rewrite an immutable publication.
POST {{pay_url}}/internal/v1/rate-publications
Content-Type: application/json
{
  "source_publication_id": "ratepub_story_hurl_12",
  "revision": 12,
  "effective_at": "2026-08-11T12:00:00Z",
  "prices": [
    {
      "model_variant_id": "variant_story_hurl_12",
      "public_model": "regional-story-model",
      "metric": "request",
      "unit_size": 1,
      "base_price_microcredits": 10,
      "discount_bps": 0,
      "customer_price_microcredits": 10
    }
  ]
}

HTTP 409
[Asserts]
jsonpath "$.error.code" == "publication_conflict"


GET {{pay_url}}/internal/v1/rate-publications/ratepub_story_hurl_12

HTTP 200
[Asserts]
jsonpath "$.source_publication_id" == "ratepub_story_hurl_12"
jsonpath "$.status" == "active"
