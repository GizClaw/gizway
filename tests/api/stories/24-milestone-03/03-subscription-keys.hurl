# M03 Subscription Key plaintext display and irreversible revocation.
# covers: account.yaml#listSubscriptionKeys account.yaml#createSubscriptionKey
# covers: account.yaml#getSubscriptionKey account.yaml#revokeSubscriptionKey

POST {{pay_url}}/account/v1/subscriptions/{{seeded_subscription_id}}/keys
Authorization: Bearer {{human_token}}
Content-Type: application/json
{}

HTTP 201
[Captures]
subscription_key_id: jsonpath "$.id"
subscription_key: jsonpath "$.key"
[Asserts]
jsonpath "$.subscription_id" == "{{seeded_subscription_id}}"
jsonpath "$.key" exists
jsonpath "$.status" == "active"
body not contains "subscription_key_hmac"


GET {{pay_url}}/account/v1/subscriptions/{{seeded_subscription_id}}/keys
Authorization: Bearer {{human_token}}

HTTP 200
[Captures]
subscription_id: jsonpath "$.data[0].subscription_id"
[Asserts]
jsonpath "$.data[?(@.id == '{{subscription_key_id}}')].key" contains "{{subscription_key}}"


GET {{pay_url}}/account/v1/subscriptions/{{subscription_id}}/keys/{{subscription_key_id}}
Authorization: Bearer {{human_token}}

HTTP 200
[Asserts]
jsonpath "$.key" == "{{subscription_key}}"


POST {{pay_url}}/account/v1/subscriptions/{{subscription_id}}/keys/{{subscription_key_id}}/revoke
Authorization: Bearer {{human_token}}

HTTP 200
[Asserts]
jsonpath "$.status" == "revoked"
jsonpath "$.revoked_at" exists
