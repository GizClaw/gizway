# Milestone 02 — regional Admin APIs accept only an active Administrator for
# the current region with the configured issuer and audience.

GET {{way_url}}/admin/v1/models
Authorization: Bearer {{admin_token}}

HTTP 200


# A normal OIDC Human is authenticated but is not a regional Administrator.
GET {{way_url}}/admin/v1/models
Authorization: Bearer {{human_token}}

HTTP 403
[Asserts]
jsonpath "$.error.code" == "administrator_required"


# A Gateway Service Account has machine operation Roles, never Admin access.
GET {{way_url}}/admin/v1/models
Authorization: Bearer {{service_token}}

HTTP 403
[Asserts]
jsonpath "$.error.code" == "administrator_required"


# A Subscription Key is not an OIDC Bearer token and cannot reach Admin APIs.
GET {{way_url}}/admin/v1/models
Authorization: Bearer {{raw_subscription_key}}

HTTP 401
[Asserts]
jsonpath "$.error.code" == "invalid_bearer_token"


# CN and Global have separate Administrator assignments even though they use
# the same ZITADEL deployment.
GET {{way_url}}/admin/v1/models
Authorization: Bearer {{other_region_admin_token}}

HTTP 403
[Asserts]
jsonpath "$.error.code" == "administrator_region_forbidden"


# A same-audience token whose Administrator Role belongs only to another
# ZITADEL Project cannot satisfy this region's exact project Role check.
GET {{way_url}}/admin/v1/models
Authorization: Bearer {{wrong_project_admin_token}}

HTTP 403
[Asserts]
jsonpath "$.error.code" == "administrator_required"


GET {{way_url}}/admin/v1/models
Authorization: Bearer {{wrong_issuer_admin_token}}

HTTP 401
[Asserts]
jsonpath "$.error.code" == "invalid_token_issuer"


GET {{way_url}}/admin/v1/models
Authorization: Bearer {{wrong_audience_admin_token}}

HTTP 401
[Asserts]
jsonpath "$.error.code" == "invalid_token_audience"


# ZITADEL token validity alone is insufficient: the local Administrator row
# must still be active.
GET {{way_url}}/admin/v1/models
Authorization: Bearer {{revoked_admin_token}}

HTTP 403
[Asserts]
jsonpath "$.error.code" == "administrator_inactive"
