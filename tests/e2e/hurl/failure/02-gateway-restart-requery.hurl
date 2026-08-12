# Compose failure profile phase two runs after `docker compose restart
# gizway-global`. Quota is intentionally process-local, so the first request
# made by the fresh process must query GizPay again and remain usable.

GET {{global_url}}/v1/models
Authorization: Bearer giz_e2e_customer_key
HTTP 200
[Asserts]
jsonpath "$.data[0].id" == "global-model"

POST {{global_url}}/v1/responses
Authorization: Bearer giz_e2e_customer_key
Content-Type: application/json
{"model":"global-model","input":"fresh process re-queried quota"}
HTTP 200
