# Story 21 — The two Admin surfaces expose only the quota facts they own.
#
# GizPay lists received UCGIDs and immutable publication receipts. GizWay lists
# its identity-free outbox and local publication state. No diagnostic response
# contains raw API keys, runtime key tokens, or an Account projection in the
# regional data. Disabling a center publication stops future ingestion while
# preserving the immutable receipt and its audit history.
# covers: gizpay-admin.yaml#listReceivedUsage gizway-admin.yaml#listUsageOutbox gizway-admin.yaml#listRatePublications gizpay-admin.yaml#listGizPayRatePublications gizpay-admin.yaml#disableRatePublication

GET {{pay_url}}/admin/v1/received_usage?region=global&limit=10
Authorization: Bearer {{admin_api_key}}

HTTP 200
[Asserts]
jsonpath "$.data" exists
jsonpath "$.page.has_more" exists


GET {{way_url}}/admin/v1/usage_outbox?status=pending&limit=10
Authorization: Bearer {{admin_api_key}}

HTTP 200
[Asserts]
jsonpath "$.data" exists
jsonpath "$.page.has_more" exists


# The regional publication is local Catalog state; this request must not be
# collapsed into the identically shaped GizPay receipt endpoint below.
GET {{way_url}}/admin/v1/rate_publications?status=active&limit=10
Authorization: Bearer {{admin_api_key}}

HTTP 200
[Asserts]
jsonpath "$.data[0].id" == "source_story_global_1"
jsonpath "$.data[0].status" == "active"


GET {{pay_url}}/admin/v1/rate_publications?node_id=gw-global-e2e&limit=10
Authorization: Bearer {{admin_api_key}}

HTTP 200
[Asserts]
jsonpath "$.data[0].id" == "ratepub_story_global_1"
jsonpath "$.data[0].status" == "active"


POST {{pay_url}}/admin/v1/rate_publications/ratepub_story_global_1/disable
Authorization: Bearer {{admin_api_key}}
Idempotency-Key: story-21-disable-rate-publication
Content-Type: application/json
{
  "reason": "operator stopped future usage"
}

HTTP 204


GET {{pay_url}}/admin/v1/rate_publications?node_id=gw-global-e2e&status=disabled
Authorization: Bearer {{admin_api_key}}

HTTP 200
[Asserts]
jsonpath "$.data[0].id" == "ratepub_story_global_1"
jsonpath "$.data[0].status" == "disabled"
