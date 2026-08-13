# A plain 409 is a real external_order_id conflict, not proof that this Outbox
# task previously committed. Only a retry marked uncertain by a transport error
# may GET the original Charge and recover it as success.

POST {{credit_spy_url}}/test/reset

HTTP 204


POST {{credit_spy_url}}/test/force-charge-conflict

HTTP 204


POST {{way_url}}/v1/chat/completions
Authorization: Bearer {{raw_subscription_key}}
Content-Type: application/json
{"model":"story-text","messages":[{"role":"user","content":"real duplicate conflict"}],"stream":false}

HTTP 200
[Asserts]
jsonpath "$.object" == "chat.completion"


GET {{way_url}}/admin/v1/charge-outbox
Authorization: Bearer {{admin_token}}
[Options]
retry: 20
retry-interval: 50ms

HTTP 200
[Asserts]
jsonpath "$.data" count == 1
jsonpath "$.data[0].status" == "abandoned"
jsonpath "$.data[0].recover_duplicate" == false


GET {{credit_spy_url}}/test/stats

HTTP 200
[Asserts]
jsonpath "$.charge_requests" > 0
jsonpath "$.charge_get_requests" == 0


# The conflict is exposed on the AI Order and never silently recovered.
GET {{way_url}}/admin/v1/ai-orders
Authorization: Bearer {{admin_token}}

HTTP 200
[Asserts]
jsonpath "$.data[0].status" == "billing_failed"
jsonpath "$.data[0].billing_error.code" == "duplicate_external_order_id"


# Remove the injected collision for test isolation. The abandoned task remains
# terminal and is not retried after the condition disappears.
DELETE {{credit_spy_url}}/test/force-charge-conflict

HTTP 204


POST {{way_url}}/test/v1/clock/advance
Content-Type: application/json
{"seconds":1}

HTTP 204


GET {{way_url}}/admin/v1/charge-outbox
Authorization: Bearer {{admin_token}}

HTTP 200
[Asserts]
jsonpath "$.data[0].status" == "abandoned"
