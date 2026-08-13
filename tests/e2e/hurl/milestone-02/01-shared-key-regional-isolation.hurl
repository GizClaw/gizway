# Milestone 02 retained-environment acceptance.
# CN and Global share ZITADEL, GizPay, Subscription and HMAC identity, while
# their Model, Provider, Provider Key, price, usage, order and outbox data stay
# regional. Bifrost is embedded in each GizWay process.

GET {{cn_url}}/v1/models
Authorization: Bearer {{raw_subscription_key}}

HTTP 200
[Asserts]
jsonpath "$.data[?(@.id == 'cn-story-model')]" count == 1
body not contains "global-story-model"


GET {{global_url}}/v1/models
Authorization: Bearer {{raw_subscription_key}}

HTTP 200
[Asserts]
jsonpath "$.data[?(@.id == 'global-story-model')]" count == 1
body not contains "cn-story-model"


POST {{cn_url}}/v1/chat/completions
Authorization: Bearer {{raw_subscription_key}}
Content-Type: application/json
{"model":"cn-story-model","messages":[{"role":"user","content":"cn"}],"stream":false}

HTTP 200
[Asserts]
jsonpath "$.model" == "cn-story-model"


POST {{global_url}}/v1/chat/completions
Authorization: Bearer {{raw_subscription_key}}
Content-Type: application/json
{"model":"global-story-model","messages":[{"role":"user","content":"global"}],"stream":false}

HTTP 200
[Asserts]
jsonpath "$.model" == "global-story-model"


# Cross-region catalog access is rejected even though the customer Key and
# central Account are shared.
POST {{cn_url}}/v1/chat/completions
Authorization: Bearer {{raw_subscription_key}}
Content-Type: application/json
{"model":"global-story-model","messages":[{"role":"user","content":"wrong region"}],"stream":false}

HTTP 404
[Asserts]
jsonpath "$.error.code" == "model_not_found"


GET {{cn_url}}/admin/v1/ai-orders
Authorization: Bearer {{cn_admin_token}}
[Options]
retry: 40
retry-interval: 100ms

HTTP 200
[Asserts]
jsonpath "$.data" count == 1
jsonpath "$.data[0].model" == "cn-story-model"


GET {{global_url}}/admin/v1/ai-orders
Authorization: Bearer {{global_admin_token}}
[Options]
retry: 40
retry-interval: 100ms

HTTP 200
[Asserts]
jsonpath "$.data" count == 1
jsonpath "$.data[0].model" == "global-story-model"


# Central GizPay sees two Charges against the same Subscription/Account.
GET {{pay_url}}/account/v1/accounts/{{payer_account_id}}/charges
Authorization: Bearer {{human_token_two}}
[Options]
retry: 40
retry-interval: 100ms

HTTP 200
[Asserts]
jsonpath "$.data" count == 2
