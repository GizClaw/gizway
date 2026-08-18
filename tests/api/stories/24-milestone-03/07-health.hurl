# M03 process-only health contract.

GET {{pay_url}}/healthz

HTTP 200
[Asserts]
jsonpath "$.status" == "healthy"
jsonpath "$.service" == "gizpay"
jsonpath "$.version" exists
jsonpath "$.revision" exists
jsonpath "$.build_time" exists
jsonpath "$.server_time" isIsoDate
body not contains "degraded"
body not contains "dependencies"
body not contains "bootstrap"

GET {{way_url}}/healthz

HTTP 200
[Asserts]
jsonpath "$.status" == "healthy"
jsonpath "$.service" == "gizway"
jsonpath "$.version" exists
jsonpath "$.revision" exists
jsonpath "$.build_time" exists
jsonpath "$.server_time" isIsoDate
body not contains "degraded"
body not contains "dependencies"
body not contains "bootstrap"
