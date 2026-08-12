# Story 14 — Usage and the next current-quota answer are exchanged together.
#
# GizPay prices raw metrics from its immutable rate publication. Usage carries
# no customer identity and no caller-computed charge. A successful response
# means every UCGID was inserted or safely recognized as an exact duplicate,
# then reports the post-transaction balance.
# covers: internal-gizpay.yaml#exchangeQuota

POST {{pay_url}}/internal/v1/quota/exchanges
Content-Type: application/json
{
  "api_key": "{{gateway_api_key}}",
  "usage": [
    {
      "ucgid": "ucg_story_14_tokens",
      "operation_id": "op_story_14_tokens",
      "public_model": "story-text",
      "model_variant_id": "91000000-0000-4000-8000-000000000001",
      "rate_publication_id": "ratepub_story_global_1",
      "metrics": {"input_token": 10, "output_token": 5},
      "started_at": "2026-08-11T12:04:50Z",
      "completed_at": "2026-08-11T12:04:52Z"
    }
  ]
}

HTTP 200
[Asserts]
jsonpath "$.status" == "allowed"
jsonpath "$.quota.microcredits" == 99999964
jsonpath "$.recheck_after_seconds" == 300
body not contains "giz_story_user_active_1"


# The same UCGID and canonical payload acknowledge a lost-response retry. It
# cannot charge a second time, and no Exchange-level Idempotency-Key is needed.
POST {{pay_url}}/internal/v1/quota/exchanges
Content-Type: application/json
{
  "api_key": "{{gateway_api_key}}",
  "usage": [
    {
      "ucgid": "ucg_story_14_tokens",
      "operation_id": "op_story_14_tokens",
      "public_model": "story-text",
      "model_variant_id": "91000000-0000-4000-8000-000000000001",
      "rate_publication_id": "ratepub_story_global_1",
      "metrics": {"input_token": 10, "output_token": 5},
      "started_at": "2026-08-11T12:04:50Z",
      "completed_at": "2026-08-11T12:04:52Z"
    }
  ]
}

HTTP 200
[Asserts]
jsonpath "$.quota.microcredits" == 99999964


# Reusing a UCGID for changed usage is a conflict. The original received Usage
# remains authoritative and the conflicting payload is never applied.
POST {{pay_url}}/internal/v1/quota/exchanges
Content-Type: application/json
{
  "api_key": "{{gateway_api_key}}",
  "usage": [
    {
      "ucgid": "ucg_story_14_tokens",
      "operation_id": "op_story_14_tokens",
      "public_model": "story-text",
      "model_variant_id": "91000000-0000-4000-8000-000000000001",
      "rate_publication_id": "ratepub_story_global_1",
      "metrics": {"input_token": 11, "output_token": 5},
      "started_at": "2026-08-11T12:04:50Z",
      "completed_at": "2026-08-11T12:04:52Z"
    }
  ]
}

HTTP 409
[Asserts]
jsonpath "$.error.code" == "ucgid_conflict"


# A structurally valid record referencing an unknown immutable price snapshot
# cannot be guessed or partially applied. A later quota-only query still works.
POST {{pay_url}}/internal/v1/quota/exchanges
Content-Type: application/json
{
  "api_key": "{{gateway_api_key}}",
  "usage": [
    {
      "ucgid": "ucg_story_14_unknown_rate",
      "operation_id": "op_story_14_unknown_rate",
      "public_model": "story-text",
      "model_variant_id": "91000000-0000-4000-8000-000000000001",
      "rate_publication_id": "ratepub_missing",
      "metrics": {"request": 1},
      "started_at": "2026-08-11T12:05:00Z",
      "completed_at": "2026-08-11T12:05:01Z"
    }
  ]
}

HTTP 422
[Asserts]
jsonpath "$.error.code" == "unpriceable_usage"


POST {{pay_url}}/internal/v1/quota/exchanges
Content-Type: application/json
{
  "api_key": "{{gateway_api_key}}"
}

HTTP 200
[Asserts]
jsonpath "$.quota.microcredits" == 99999964
