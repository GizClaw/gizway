# Milestone 02 — Gateway Credit state is process-local, current-balance based,
# singleflight per HMAC, and decremented only by completed actual Gross.
# This story uses a transparent counting proxy in front of real GizPay.

POST {{credit_spy_url}}/test/reset

HTTP 204


# The fixture Account starts at 10. One call costs actual Gross 7.
POST {{way_url}}/v1/chat/completions
Authorization: Bearer {{raw_subscription_key_quota_10}}
Content-Type: application/json
{"model":"story-text","messages":[{"role":"user","content":"first actual gross seven"}],"stream":false}

HTTP 200


GET {{credit_spy_url}}/test/stats

HTTP 200
[Asserts]
jsonpath "$.credit_check_requests" == 1


# Remaining local Credit is 3 (>0), so a second request starts without another
# central query. There is no request-start estimate or reservation.
POST {{way_url}}/v1/chat/completions
Authorization: Bearer {{raw_subscription_key_quota_10}}
Content-Type: application/json
{"model":"story-text","messages":[{"role":"user","content":"second actual gross seven"}],"stream":false}

HTTP 200


GET {{credit_spy_url}}/test/stats

HTTP 200
[Asserts]
jsonpath "$.credit_check_requests" == 1


# Charge delivery is asynchronous. Wait until both completed calls have reached
# GizPay before asserting the result of the next central Credit Check.
GET {{way_url}}/admin/v1/charge-outbox
Authorization: Bearer {{admin_token}}
[Options]
retry: 40
retry-interval: 50ms

HTTP 200
[Asserts]
jsonpath "$.data[?(@.status == 'reported')]" count == 2


# Two completed Gross-7 calls atomically changed local 10 -> 3 -> -4. The next
# service start must requery GizPay and receive central denial.
POST {{way_url}}/v1/chat/completions
Authorization: Bearer {{raw_subscription_key_quota_10}}
Content-Type: application/json
{"model":"story-text","messages":[{"role":"user","content":"must recheck depleted credit"}],"stream":false}

HTTP 402
[Asserts]
jsonpath "$.error.code" == "credit_denied"


GET {{credit_spy_url}}/test/stats

HTTP 200
[Asserts]
jsonpath "$.credit_check_requests" == 2


POST {{credit_spy_url}}/test/reset

HTTP 204


# Model discovery warms a different HMAC without deducting Credit.
GET {{way_url}}/v1/models
Authorization: Bearer {{raw_subscription_key_quota_100}}

HTTP 200


GET {{credit_spy_url}}/test/stats

HTTP 200
[Asserts]
jsonpath "$.credit_check_requests" == 1


# Story-test clock control avoids a five-minute sleep while advancing the same
# monotonic business clock used by local Credit expiry.
POST {{way_url}}/test/v1/clock/advance
Content-Type: application/json
{"seconds":301}

HTTP 204


GET {{way_url}}/v1/models
Authorization: Bearer {{raw_subscription_key_quota_100}}

HTTP 200


GET {{credit_spy_url}}/test/stats

HTTP 200
[Asserts]
jsonpath "$.credit_check_requests" == 2


POST {{credit_spy_url}}/test/reset

HTTP 204


# Four requests enter the Provider concurrently and are held at a barrier until
# all four have passed admission. Cold-start Credit Check is singleflight.
POST {{fake_url}}/drive-concurrent-chat
Content-Type: application/json
{
  "api_url":"{{way_url}}",
  "api_key":"{{raw_subscription_key_quota_concurrent_20}}",
  "model":"story-text",
  "message":"concurrent-barrier",
  "count":4
}

HTTP 200
[Asserts]
jsonpath "$.requested" == 4
jsonpath "$.succeeded" == 4
jsonpath "$.failed" == 0


GET {{credit_spy_url}}/test/stats

HTTP 200
[Asserts]
jsonpath "$.credit_check_requests" == 1


# Atomic completion applies all four Gross-7 deductions: 20 -> -8. A lost
# update would incorrectly leave positive Credit and skip this second query.
GET {{way_url}}/admin/v1/charge-outbox
Authorization: Bearer {{admin_token}}
[Options]
retry: 40
retry-interval: 50ms

HTTP 200
[Asserts]
jsonpath "$.data[?(@.status == 'reported')]" count == 6


POST {{way_url}}/v1/chat/completions
Authorization: Bearer {{raw_subscription_key_quota_concurrent_20}}
Content-Type: application/json
{"model":"story-text","messages":[{"role":"user","content":"verify atomic completion"}],"stream":false}

HTTP 402
[Asserts]
jsonpath "$.error.code" == "credit_denied"


GET {{credit_spy_url}}/test/stats

HTTP 200
[Asserts]
jsonpath "$.credit_check_requests" == 2
