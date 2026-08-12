# Compose failure profile phase one: a real TCP fault is introduced between
# GizWay and GizPay. The Gateway fails the fresh quota query, then recovers;
# a cut response after GizPay commits leaves Usage retryable rather than lost.

POST {{global_url}}/admin/v1/auth/login
Idempotency-Key: e2e-failure-global-login
Content-Type: application/json
{
  "email": "global-admin@e2e.invalid",
  "password": "e2e-password"
}
HTTP 200
[Captures]
global_admin: jsonpath "$.access_token"

POST {{global_url}}/admin/v1/rate_publications
Authorization: Bearer {{global_admin}}
Idempotency-Key: e2e-failure-publication
Content-Type: application/json
{
  "source_publication_id": "e2e-failure-publication",
  "effective_at": "2026-08-11T00:00:00Z"
}
HTTP 201

POST {{toxiproxy_url}}/proxies/gizpay
Content-Type: application/json
{"enabled": false}
HTTP 200

GET {{global_url}}/v1/models
Authorization: Bearer giz_e2e_customer_key
HTTP 503

POST {{toxiproxy_url}}/proxies/gizpay
Content-Type: application/json
{"enabled": true}
HTTP 200

GET {{global_url}}/v1/models
Authorization: Bearer giz_e2e_customer_key
HTTP 200

POST {{global_url}}/v1/responses
Authorization: Bearer giz_e2e_customer_key
Content-Type: application/json
{"model":"global-model","input":"Usage awaiting response-loss retry"}
HTTP 200

# Cut only the downstream bytes. GizPay receives and commits the Usage batch,
# while GizWay observes a temporary response failure and retains retry state.
POST {{toxiproxy_url}}/proxies/gizpay/toxics
Content-Type: application/json
{
  "name": "cut-exchange-response",
  "type": "limit_data",
  "stream": "downstream",
  "toxicity": 1.0,
  "attributes": {"bytes": 0}
}
HTTP 200

GET {{global_url}}/v1/models
Authorization: Bearer giz_e2e_customer_key
HTTP 503

DELETE {{toxiproxy_url}}/proxies/gizpay/toxics/cut-exchange-response
HTTP 204

# A later request/retry observes the already committed UCGID and receives a
# fresh quota answer without charging the Usage twice.
GET {{global_url}}/v1/models
Authorization: Bearer giz_e2e_customer_key
HTTP 200

POST {{pay_url}}/admin/v1/auth/login
Idempotency-Key: e2e-failure-pay-login
Content-Type: application/json
{
  "email": "admin@e2e.invalid",
  "password": "e2e-password"
}
HTTP 200
[Captures]
pay_admin: jsonpath "$.access_token"

GET {{pay_url}}/admin/v1/received_usage?region=global
Authorization: Bearer {{pay_admin}}
HTTP 200
[Asserts]
jsonpath "$.data" count == 1
