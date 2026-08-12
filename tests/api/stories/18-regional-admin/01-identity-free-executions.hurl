# Story 18 — Regional Admin exposes local executions, not customer identity.
#
# The request is sent to a real GizWay process backed by its regional-only
# database. Account and API-key filters are intentionally not part of the
# operation.
# covers: gizway-admin.yaml#listGatewayExecutions gizway-admin.yaml#getGatewayExecution

GET {{way_url}}/admin/v1/gateway_executions?limit=1
Authorization: Bearer {{admin_api_key}}

HTTP 200
[Asserts]
jsonpath "$.data" exists
jsonpath "$.page.has_more" exists


GET {{way_url}}/admin/v1/gateway_executions/00000000-0000-4000-8000-000000000000
Authorization: Bearer {{admin_api_key}}

HTTP 404
[Asserts]
jsonpath "$.error.code" == "not_found"
