# Admin authentication, idempotency, conflict, dependency, and redaction failures.

GET {{pay_url}}/admin/v1/products

HTTP 401
[Asserts]
jsonpath "$.error.code" == "invalid_admin_key"


GET {{way_url}}/admin/v1/providers
X-GizWay-Admin-Key: wrong-admin-key

HTTP 401
[Asserts]
jsonpath "$.error.code" == "invalid_admin_key"


POST {{pay_url}}/admin/v1/products
X-GizWay-Admin-Key: {{admin_key}}
Content-Type: application/json
{
  "id": "product_admin_idempotent",
  "merchant_id": "{{default_merchant_id}}",
  "name": "Admin Idempotent PAYG",
  "billing_mode": "pay_as_you_go",
  "published": true,
  "status": "active",
  "terms_version": "admin-v1"
}

HTTP 201


POST {{pay_url}}/admin/v1/products
X-GizWay-Admin-Key: {{admin_key}}
Content-Type: application/json
{
  "id": "product_admin_idempotent",
  "merchant_id": "{{default_merchant_id}}",
  "name": "Admin Idempotent PAYG",
  "billing_mode": "pay_as_you_go",
  "published": true,
  "status": "active",
  "terms_version": "admin-v1"
}

HTTP 200


POST {{pay_url}}/admin/v1/products
X-GizWay-Admin-Key: {{admin_key}}
Content-Type: application/json
{
  "id": "product_admin_idempotent",
  "merchant_id": "merchant_conflict",
  "name": "Admin Idempotent PAYG",
  "billing_mode": "pay_as_you_go",
  "published": true,
  "status": "active",
  "terms_version": "admin-v1"
}

HTTP 409
[Asserts]
jsonpath "$.error.code" == "resource_id_conflict"


POST {{way_url}}/admin/v1/models
X-GizWay-Admin-Key: {{admin_key}}
Content-Type: application/json
{
  "id": "model_admin_missing_provider",
  "provider_id": "provider_missing",
  "name": "missing-provider-model",
  "provider_model": "missing-provider-model",
  "status": "active"
}

HTTP 404
[Asserts]
jsonpath "$.error.code" == "dependency_not_found"


DELETE {{pay_url}}/admin/v1/products/product_admin_idempotent
X-GizWay-Admin-Key: {{admin_key}}

HTTP 204


DELETE {{pay_url}}/admin/v1/products/product_admin_idempotent
X-GizWay-Admin-Key: {{admin_key}}

HTTP 204
