# Story 08 — Provider and endpoint lifecycle with write-only credentials.
#
# Product behavior:
# - Provider identity and routing endpoint are database records, not process
#   constants. Disabling either removes it from future routing without deleting
#   historical model/usage references.
# - Upstream credentials are accepted only on create/rotate. The API stores a
#   one-way secret reference for this slice and never returns plaintext, hashes
#   or internal credential references.
# - Every mutation is attributed to the authenticated administrator.
#
# Fixture: story-base administrator key.
# Isolation: fresh PostgreSQL per file.
# covers: listProviders createProvider updateProvider listProviderEndpoints
# covers: createProviderEndpoint updateProviderEndpoint rotateProviderCredential

GET {{base_url}}/admin/v1/providers
Authorization: Bearer {{admin_api_key}}

HTTP 200
[Asserts]
jsonpath "$.data" count == 1


POST {{base_url}}/admin/v1/providers
Authorization: Bearer {{admin_api_key}}
Idempotency-Key: story-08-provider
Content-Type: application/json
{
  "slug": "story-secondary",
  "name": "Story Secondary Provider"
}

HTTP 201
[Captures]
provider_id: jsonpath "$.id"
[Asserts]
jsonpath "$.status" == "active"


POST {{base_url}}/admin/v1/providers/{{provider_id}}/endpoints
Authorization: Bearer {{admin_api_key}}
Idempotency-Key: story-08-provider-endpoint
Content-Type: application/json
{
  "name": "Secondary US",
  "base_url": "https://provider.gizway.test",
  "credential": "must-never-be-returned",
  "region": "us",
  "priority": 20,
  "weight": 50
}

HTTP 201
[Captures]
endpoint_id: jsonpath "$.id"
[Asserts]
jsonpath "$.credential_configured" == true
jsonpath "$.credential" not exists
jsonpath "$.credential_ref" not exists


# Endpoint routing fields are validated before SQL. Malformed public URLs and
# negative priorities produce a stable 400 rather than a driver error or a
# catalog row that can fail only during customer traffic.
POST {{base_url}}/admin/v1/providers/{{provider_id}}/endpoints
Authorization: Bearer {{admin_api_key}}
Idempotency-Key: story-08-invalid-provider-endpoint
Content-Type: application/json
{
  "name": "Invalid endpoint",
  "base_url": "https://user:secret@provider.gizway.test",
  "credential": "not-persisted",
  "priority": -1,
  "weight": 50
}

HTTP 400
[Asserts]
jsonpath "$.error.code" == "invalid_request"


POST {{base_url}}/admin/v1/provider_endpoints/{{endpoint_id}}/rotate_credential
Authorization: Bearer {{admin_api_key}}
Idempotency-Key: story-08-rotate-provider-secret
Content-Type: application/json
{
  "credential": "new-secret-also-never-returned"
}

HTTP 204


PATCH {{base_url}}/admin/v1/provider_endpoints/{{endpoint_id}}
Authorization: Bearer {{admin_api_key}}
Idempotency-Key: story-08-update-endpoint
Content-Type: application/json
{
  "name": "Secondary US Draining",
  "base_url": "https://provider.gizway.test",
  "region": "us-east-2",
  "priority": 0,
  "weight": 50,
  "status": "draining"
}

HTTP 200
[Asserts]
jsonpath "$.status" == "draining"
jsonpath "$.region" == "us-east-2"
jsonpath "$.priority" == 0
jsonpath "$.credential_ref" not exists


GET {{base_url}}/admin/v1/providers/{{provider_id}}/endpoints
Authorization: Bearer {{admin_api_key}}

HTTP 200
[Asserts]
jsonpath "$.data" count == 1
jsonpath "$.data[0].credential_configured" == true
jsonpath "$.data[0].region" == "us-east-2"
jsonpath "$.data[0].priority" == 0


PATCH {{base_url}}/admin/v1/providers/{{provider_id}}
Authorization: Bearer {{admin_api_key}}
Idempotency-Key: story-08-disable-provider
Content-Type: application/json
{
  "name": "Story Secondary Provider Disabled",
  "status": "disabled"
}

HTTP 200
[Asserts]
jsonpath "$.status" == "disabled"
