# Run this story after restarting only the Global GizWay process.
# Credit state is memory-only: the first post-restart request must perform a
# new central Credit Check instead of restoring a regional quota table.

# Break only the central link after the regional process has restarted. A
# wrongly restored or persisted Quota would let the request pass; the required
# fresh Credit Check makes it fail closed.
POST {{toxiproxy_url}}/proxies/gizpay/toxics
Content-Type: application/json
{
  "name": "prove-post-restart-credit-check",
  "type": "reset_peer",
  "stream": "downstream",
  "toxicity": 1.0,
  "attributes": {"timeout": 0}
}

HTTP 200


POST {{global_url}}/v1/chat/completions
Authorization: Bearer {{raw_subscription_key}}
Content-Type: application/json
{"model":"global-story-model","messages":[{"role":"user","content":"after restart"}],"stream":false}

HTTP 503
[Asserts]
jsonpath "$.error.code" == "credit_check_unavailable"


DELETE {{toxiproxy_url}}/proxies/gizpay/toxics/prove-post-restart-credit-check

HTTP 204


# Restoring GizPay allows the same first post-restart admission to rebuild
# memory state and complete normally.
POST {{global_url}}/v1/chat/completions
Authorization: Bearer {{raw_subscription_key}}
Content-Type: application/json
{"model":"global-story-model","messages":[{"role":"user","content":"after restart retry"}],"stream":false}

HTTP 200
[Asserts]
jsonpath "$.model" == "global-story-model"


GET {{global_url}}/admin/v1/charge-outbox
Authorization: Bearer {{global_admin_token}}
[Options]
retry: 40
retry-interval: 100ms

HTTP 200
[Asserts]
jsonpath "$.data[?(@.status == 'reported')]" count == 1
