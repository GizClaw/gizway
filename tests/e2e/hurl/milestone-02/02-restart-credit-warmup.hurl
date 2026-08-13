# This file runs before restarting Global GizWay. Model discovery must perform
# Credit Check but creates no AI Order, Charge Outbox row, or central Charge.

GET {{global_url}}/v1/models
Authorization: Bearer {{raw_subscription_key}}

HTTP 200
[Asserts]
jsonpath "$.data[?(@.id == 'global-story-model')]" count == 1
