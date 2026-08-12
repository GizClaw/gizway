# Story 15 — Retry safety belongs to individual UCGIDs in the current process.
#
# This executable boundary rejects client identity copies, final-charge claims,
# and oversized batches. A bad batch is atomic: none of its otherwise valid
# records may be charged. Cross-restart recovery and historical backcharge are
# intentionally outside this version.
# covers: internal-gizpay.yaml#exchangeQuota

POST {{pay_url}}/internal/v1/quota/exchanges
Content-Type: application/json
{
  "api_key": "{{gateway_api_key}}",
  "usage": [
    {
      "ucgid": "ucg_story_15_bad_shape",
      "operation_id": "op_story_15_bad_shape",
      "public_model": "story-text",
      "model_variant_id": "91000000-0000-4000-8000-000000000001",
      "rate_publication_id": "ratepub_story_global_1",
      "metrics": {"request": 1},
      "charged_microcredits": 9,
      "started_at": "2026-08-11T12:05:00Z",
      "completed_at": "2026-08-11T12:05:01Z"
    }
  ]
}

HTTP 400
[Asserts]
jsonpath "$.error.code" == "invalid_request"


# A later valid use of the same UCGID succeeds, proving validation did not
# reserve the identity or leave a partial financial mutation.
POST {{pay_url}}/internal/v1/quota/exchanges
Content-Type: application/json
{
  "api_key": "{{gateway_api_key}}",
  "usage": [
    {
      "ucgid": "ucg_story_15_bad_shape",
      "operation_id": "op_story_15_bad_shape",
      "public_model": "story-text",
      "model_variant_id": "91000000-0000-4000-8000-000000000001",
      "rate_publication_id": "ratepub_story_global_1",
      "metrics": {"request": 1},
      "started_at": "2026-08-11T12:05:00Z",
      "completed_at": "2026-08-11T12:05:01Z"
    }
  ]
}

HTTP 200
[Asserts]
jsonpath "$.quota.microcredits" == 99999991
