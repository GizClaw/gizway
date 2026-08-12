# Story 20 — A regional rate cannot become active without GizPay confirmation.
#
# The real GizWay test process publishes through its mTLS GizPay client. The
# local publication becomes active only after GizPay returns its immutable
# receipt.
# covers: gizway-admin.yaml#publishRegionalRatePublication

POST {{way_url}}/admin/v1/rate_publications
Authorization: Bearer {{admin_api_key}}
Idempotency-Key: story-20-rate-publication
Content-Type: application/json
{
  "source_publication_id": "story-regional-source-20",
  "effective_at": "2026-08-11T12:00:00Z"
}

HTTP 201
[Asserts]
jsonpath "$.status" == "active"
jsonpath "$.gizpay_publication_id" exists
