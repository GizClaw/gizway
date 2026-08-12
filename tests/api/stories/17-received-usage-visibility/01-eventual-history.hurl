# Story 17 — Account history contains only Usage GizPay has actually received.
#
# There is no completeness watermark or claim that Gateway pending Usage is in
# the center. The response simply reports eventual consistency and the center's
# own query time. Once Exchange commits a record, history exposes that record.
# covers: internal-gizpay.yaml#exchangeQuota account.yaml#listGatewayUsage account.yaml#getAccountBalance

GET {{pay_url}}/account/v1/accounts/21000000-0000-4000-8000-000000000001/usage?from=2026-08-10T00%3A00%3A00Z&to=2027-08-11T00%3A00%3A00Z
Authorization: Bearer {{active_user_one_key}}

HTTP 200
[Asserts]
jsonpath "$.data" count == 0
jsonpath "$.consistency.mode" == "eventual"
jsonpath "$.consistency.recent_usage_may_be_pending" == true
jsonpath "$.consistency.as_of" exists


GET {{pay_url}}/account/v1/accounts/21000000-0000-4000-8000-000000000001/balance
Authorization: Bearer {{active_user_one_key}}

HTTP 200
[Asserts]
jsonpath "$.posted_microcredits" == 100000000
jsonpath "$.ai_spendable_microcredits" == 100000000


POST {{pay_url}}/internal/v1/quota/exchanges
Content-Type: application/json
{
  "api_key": "{{gateway_api_key}}",
  "usage": [
    {
      "ucgid": "ucg_story_17_visible",
      "operation_id": "op_story_17_visible",
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


GET {{pay_url}}/account/v1/accounts/21000000-0000-4000-8000-000000000001/usage?from=2026-08-10T00%3A00%3A00Z&to=2027-08-11T00%3A00%3A00Z
Authorization: Bearer {{active_user_one_key}}

HTTP 200
[Asserts]
jsonpath "$.data" count == 1
jsonpath "$.data[0].ucgid" == "ucg_story_17_visible"
jsonpath "$.data[0].charged_microcredits" == 9
jsonpath "$.consistency.mode" == "eventual"
jsonpath "$.consistency.recent_usage_may_be_pending" == true
jsonpath "$.consistency.as_of" exists
