# Story 18 — Every GizWay region owns an independent administrator system.
#
# This deliberately repeats the identity operations tested against GizPay,
# but sends them to way_url. Equal paths and operation IDs do not make the two
# databases or deployed services interchangeable.
# covers: gizway-admin.yaml#loginAdministrator gizway-admin.yaml#refreshAdministratorSession gizway-admin.yaml#logoutAdministrator gizway-admin.yaml#getCurrentAdministrator
# covers: gizway-admin.yaml#listAdministrators gizway-admin.yaml#createAdministrator gizway-admin.yaml#getAdministrator gizway-admin.yaml#updateAdministrator
# covers: gizway-admin.yaml#listAdministratorApiKeys gizway-admin.yaml#createAdministratorApiKey gizway-admin.yaml#revokeAdministratorApiKey

POST {{way_url}}/admin/v1/auth/login
Idempotency-Key: story-18-regional-admin-login
Content-Type: application/json
{
  "email": "admin@gizway.test",
  "password": "story-admin-password"
}

HTTP 200
[Captures]
regional_admin_session: jsonpath "$.access_token"
[Asserts]
jsonpath "$.administrator.id" == "41000000-0000-4000-8000-000000000001"


GET {{way_url}}/admin/v1/me
Authorization: Bearer {{regional_admin_session}}

HTTP 200
[Asserts]
jsonpath "$.email" == "admin@gizway.test"


POST {{way_url}}/admin/v1/administrators
Authorization: Bearer {{regional_admin_session}}
Idempotency-Key: story-18-create-regional-admin
Content-Type: application/json
{
  "email": "second-regional-admin@gizway.test",
  "display_name": "Second Regional Administrator",
  "password": "second-regional-password"
}

HTTP 201
[Captures]
regional_admin_id: jsonpath "$.id"


GET {{way_url}}/admin/v1/administrators
Authorization: Bearer {{admin_api_key}}

HTTP 200
[Asserts]
jsonpath "$.data" count == 2


GET {{way_url}}/admin/v1/administrators/{{regional_admin_id}}
Authorization: Bearer {{admin_api_key}}

HTTP 200
[Asserts]
jsonpath "$.status" == "active"


PATCH {{way_url}}/admin/v1/administrators/{{regional_admin_id}}
Authorization: Bearer {{admin_api_key}}
Idempotency-Key: story-18-update-regional-admin
Content-Type: application/json
{
  "display_name": "Updated Regional Administrator",
  "reason": "regional identity test"
}

HTTP 200
[Asserts]
jsonpath "$.display_name" == "Updated Regional Administrator"


POST {{way_url}}/admin/v1/administrators/{{regional_admin_id}}/api_keys
Authorization: Bearer {{admin_api_key}}
Idempotency-Key: story-18-create-regional-admin-key
Content-Type: application/json
{
  "name": "Regional automation"
}

HTTP 201
[Captures]
regional_admin_key_id: jsonpath "$.id"
[Asserts]
jsonpath "$.secret" startsWith "gizadm_"


GET {{way_url}}/admin/v1/administrators/{{regional_admin_id}}/api_keys
Authorization: Bearer {{admin_api_key}}

HTTP 200
[Asserts]
jsonpath "$.data" count == 1


POST {{way_url}}/admin/v1/administrators/{{regional_admin_id}}/api_keys/{{regional_admin_key_id}}/revoke
Authorization: Bearer {{admin_api_key}}
Idempotency-Key: story-18-revoke-regional-admin-key
Content-Type: application/json
{
  "reason": "rotation"
}

HTTP 204


POST {{way_url}}/admin/v1/auth/refresh
Authorization: Bearer {{regional_admin_session}}
Idempotency-Key: story-18-refresh-regional-admin

HTTP 200
[Captures]
regional_admin_session_rotated: jsonpath "$.access_token"


POST {{way_url}}/admin/v1/auth/logout
Authorization: Bearer {{regional_admin_session_rotated}}
Idempotency-Key: story-18-logout-regional-admin

HTTP 204


GET {{way_url}}/admin/v1/me
Authorization: Bearer {{regional_admin_session_rotated}}

HTTP 401
