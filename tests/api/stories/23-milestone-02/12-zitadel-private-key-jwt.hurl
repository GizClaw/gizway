# Milestone 02 — Gateway obtains its GizPay Service Account token from ZITADEL
# with ZITADEL's RFC 7523 JWT bearer grant and a signed Private Key JWT.

POST {{oauth_spy_url}}/test/reset

HTTP 204


# A fresh Gateway process has no cached access token. Its first customer API
# request must exchange the configured machine assertion before Credit Check.
GET {{way_url}}/v1/models
Authorization: Bearer {{raw_subscription_key}}

HTTP 200


GET {{oauth_spy_url}}/test/stats

HTTP 200
[Asserts]
jsonpath "$.token_requests" == 1
jsonpath "$.grant_type" == "urn:ietf:params:oauth:grant-type:jwt-bearer"
jsonpath "$.assertion_header.alg" == "RS256"
jsonpath "$.assertion_header.kid" exists
jsonpath "$.assertion_claims.iss" exists
jsonpath "$.assertion_claims.sub" exists
jsonpath "$.assertion_claims.aud" exists
jsonpath "$.assertion_claims.iat" exists
jsonpath "$.assertion_claims.exp" exists
body not contains "PRIVATE KEY"


# The short-lived access token is reused until its refresh boundary; a second
# operation must not create a second assertion immediately.
GET {{way_url}}/v1/models
Authorization: Bearer {{raw_subscription_key}}

HTTP 200


GET {{oauth_spy_url}}/test/stats

HTTP 200
[Asserts]
jsonpath "$.token_requests" == 1
