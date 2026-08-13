# Run after stopping only postgres-global. A failed local database/schema check
# is unhealthy even though the HTTP process is still reachable.

GET {{global_url}}/healthz

HTTP 503
[Asserts]
jsonpath "$.status" == "unhealthy"
jsonpath "$.service.kind" == "gizway"
body not contains "postgres://"
body not contains "password"
body not contains "access_token"
body not contains "PRIVATE KEY"
