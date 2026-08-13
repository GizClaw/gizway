# Health is computed from local database/startup state and cached dependency
# observations. It must not synchronously call ZITADEL, GizPay, or a Provider.

POST {{credit_spy_url}}/test/reset

HTTP 204


POST {{oauth_spy_url}}/test/reset

HTTP 204


POST {{fake_url}}/test/reset

HTTP 204


# A successful local API request and a successful Credit Check observation do
# not turn an incomplete regional bootstrap into a healthy one.
GET {{incomplete_url}}/v1/models
Authorization: Bearer {{raw_subscription_key}}

HTTP 200
[Asserts]
jsonpath "$.data" count == 0


GET {{incomplete_url}}/healthz

HTTP 200
[Asserts]
jsonpath "$.status" == "degraded"
jsonpath "$.bootstrap.status" == "incomplete"


# Reset dependency spies after the successful request. The remaining counters
# therefore measure Health fanout only.
POST {{credit_spy_url}}/test/reset

HTTP 204


POST {{oauth_spy_url}}/test/reset

HTTP 204


POST {{fake_url}}/test/reset

HTTP 204


GET {{global_url}}/healthz

HTTP 200
[Asserts]
jsonpath "$.status" matches "^(healthy|degraded)$"
jsonpath "$.service.kind" == "gizway"
body not contains "postgres://"
body not contains "access_token"
body not contains "PRIVATE KEY"


# Missing Model/Provider/billing bootstrap data is a degraded business state,
# not a local-process failure.
GET {{incomplete_url}}/healthz

HTTP 200
[Asserts]
jsonpath "$.status" == "degraded"
jsonpath "$.bootstrap.status" == "incomplete"


GET {{pay_url}}/healthz

HTTP 200
[Asserts]
jsonpath "$.status" matches "^(healthy|degraded)$"


GET {{credit_spy_url}}/test/stats

HTTP 200
[Asserts]
jsonpath "$.total_requests" == 0


GET {{oauth_spy_url}}/test/stats

HTTP 200
[Asserts]
jsonpath "$.total_requests" == 0
jsonpath "$.token_requests" == 0


GET {{fake_url}}/test/stats

HTTP 200
[Asserts]
jsonpath "$.total_requests" == 0
