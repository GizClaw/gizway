# Milestone 02 failure acceptance.
# Warm Credit first, then ask the transparent Credit Spy to forward and commit
# exactly one Charge while dropping its response connection. The same persisted
# Outbox task retries, receives 409, and must preserve its uncertain state while
# recovery GET is temporarily unavailable.

GET {{global_url}}/v1/models
Authorization: Bearer {{raw_subscription_key}}

HTTP 200


POST {{credit_spy_url}}/test/drop-next-charge-response

HTTP 204


# Keep the first recovery GET temporarily unavailable after the POST response
# path is restored. The Outbox must remain uncertain rather than turning the
# following 409 into a real business conflict.
POST {{credit_spy_url}}/test/fail-charge-get

HTTP 204


POST {{global_url}}/v1/chat/completions
Authorization: Bearer {{raw_subscription_key}}
Content-Type: application/json
{"model":"global-story-model","messages":[{"role":"user","content":"response loss"}],"stream":false}

HTTP 200


GET {{global_url}}/admin/v1/charge-outbox
Authorization: Bearer {{global_admin_token}}
[Options]
retry: 60
retry-interval: 100ms

HTTP 200
[Asserts]
jsonpath "$.data" count == 1
jsonpath "$.data[0].status" == "pending"
jsonpath "$.data[0].recover_duplicate" == true


GET {{credit_spy_url}}/test/stats
[Options]
retry: 60
retry-interval: 100ms

HTTP 200
[Asserts]
jsonpath "$.charge_get_requests" > 0


DELETE {{credit_spy_url}}/test/fail-charge-get

HTTP 204


GET {{global_url}}/admin/v1/charge-outbox
Authorization: Bearer {{global_admin_token}}
[Options]
retry: 60
retry-interval: 100ms

HTTP 200
[Asserts]
jsonpath "$.data" count == 1
jsonpath "$.data[0].status" == "reported"
jsonpath "$.data[0].recover_duplicate" == false


GET {{pay_url}}/account/v1/accounts/{{payer_account_id}}/charges
Authorization: Bearer {{human_token_two}}

HTTP 200
[Asserts]
jsonpath "$.data" count == 1
