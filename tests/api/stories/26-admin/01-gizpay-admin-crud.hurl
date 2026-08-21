# GizPay Admin CRUD over the configured initial Admin Key.
# covers: gizpay-admin.yaml#createAdminProduct gizpay-admin.yaml#listAdminProducts gizpay-admin.yaml#getAdminProduct
# covers: gizpay-admin.yaml#updateAdminProduct gizpay-admin.yaml#deleteAdminProduct
# covers: gizpay-admin.yaml#createAdminProductListing gizpay-admin.yaml#listAdminProductListings gizpay-admin.yaml#getAdminProductListing
# covers: gizpay-admin.yaml#updateAdminProductListing gizpay-admin.yaml#deleteAdminProductListing
# covers: gizpay-admin.yaml#createAdminServicePrincipal gizpay-admin.yaml#listAdminServicePrincipals gizpay-admin.yaml#getAdminServicePrincipal
# covers: gizpay-admin.yaml#updateAdminServicePrincipal gizpay-admin.yaml#deleteAdminServicePrincipal

POST {{pay_internal_url}}/admin/v1/products
X-GizWay-Admin-Key: {{admin_key}}
Content-Type: application/json
{
  "id": "product_admin_hurl",
  "merchant_id": "{{default_merchant_id}}",
  "name": "Admin Hurl PAYG",
  "billing_mode": "pay_as_you_go",
  "published": true,
  "status": "active",
  "terms_version": "admin-v1"
}

HTTP 201
[Asserts]
jsonpath "$.id" == "product_admin_hurl"
jsonpath "$.merchant_id" == "{{default_merchant_id}}"


GET {{pay_internal_url}}/admin/v1/products
X-GizWay-Admin-Key: {{admin_key}}

HTTP 200
[Asserts]
jsonpath "$.data[?(@.id == 'product_admin_hurl')].name" contains "Admin Hurl PAYG"


GET {{pay_internal_url}}/admin/v1/products/product_admin_hurl
X-GizWay-Admin-Key: {{admin_key}}

HTTP 200
[Asserts]
jsonpath "$.status" == "active"


PATCH {{pay_internal_url}}/admin/v1/products/product_admin_hurl
X-GizWay-Admin-Key: {{admin_key}}
Content-Type: application/json
{
  "name": "Admin Hurl PAYG Updated",
  "terms_version": "admin-v2"
}

HTTP 200
[Asserts]
jsonpath "$.name" == "Admin Hurl PAYG Updated"
jsonpath "$.terms_version" == "admin-v2"


POST {{pay_internal_url}}/admin/v1/product-listings
X-GizWay-Admin-Key: {{admin_key}}
Content-Type: application/json
{
  "id": "product_listing_admin_hurl",
  "product_id": "product_admin_hurl",
  "site": "admin.localhost",
  "title": "Admin Product",
  "description": "Admin Product Listing",
  "billing_mode": "pay_as_you_go",
  "price_text": "Usage based",
  "display_order": 10,
  "status": "active"
}

HTTP 201
[Asserts]
jsonpath "$.id" == "product_listing_admin_hurl"


GET {{pay_internal_url}}/admin/v1/product-listings
X-GizWay-Admin-Key: {{admin_key}}

HTTP 200
[Asserts]
jsonpath "$.data[?(@.id == 'product_listing_admin_hurl')].site" contains "admin.localhost"


GET {{pay_internal_url}}/admin/v1/product-listings/product_listing_admin_hurl
X-GizWay-Admin-Key: {{admin_key}}

HTTP 200
[Asserts]
jsonpath "$.product_id" == "product_admin_hurl"


PATCH {{pay_internal_url}}/admin/v1/product-listings/product_listing_admin_hurl
X-GizWay-Admin-Key: {{admin_key}}
Content-Type: application/json
{
  "title": "Admin Product Updated",
  "display_order": 11
}

HTTP 200
[Asserts]
jsonpath "$.title" == "Admin Product Updated"
jsonpath "$.display_order" == 11


POST {{pay_internal_url}}/admin/v1/service-principals
X-GizWay-Admin-Key: {{admin_key}}
Content-Type: application/json
{
  "id": "service_principal_admin_hurl",
  "owner_identity_issuer": "https://identity.e2e.gizclaw.test:18080",
  "owner_identity_subject": "{{human_subject}}",
  "identity_issuer": "https://identity.e2e.gizclaw.test:18080",
  "identity_subject": "{{service_reader_subject}}",
  "name": "Admin Hurl Service",
  "roles": ["charge"],
  "status": "active"
}

HTTP 201
[Asserts]
jsonpath "$.id" == "service_principal_admin_hurl"


GET {{pay_internal_url}}/admin/v1/service-principals
X-GizWay-Admin-Key: {{admin_key}}

HTTP 200
[Asserts]
jsonpath "$.data[?(@.id == 'service_principal_admin_hurl')].name" contains "Admin Hurl Service"


GET {{pay_internal_url}}/admin/v1/service-principals/service_principal_admin_hurl
X-GizWay-Admin-Key: {{admin_key}}

HTTP 200
[Asserts]
jsonpath "$.identity_subject" == "{{service_reader_subject}}"


PATCH {{pay_internal_url}}/admin/v1/service-principals/service_principal_admin_hurl
X-GizWay-Admin-Key: {{admin_key}}
Content-Type: application/json
{
  "name": "Admin Hurl Service Updated",
  "roles": ["credit_check", "charge"]
}

HTTP 200
[Asserts]
jsonpath "$.name" == "Admin Hurl Service Updated"


DELETE {{pay_internal_url}}/admin/v1/service-principals/service_principal_admin_hurl
X-GizWay-Admin-Key: {{admin_key}}

HTTP 204


DELETE {{pay_internal_url}}/admin/v1/product-listings/product_listing_admin_hurl
X-GizWay-Admin-Key: {{admin_key}}

HTTP 204


DELETE {{pay_internal_url}}/admin/v1/products/product_admin_hurl
X-GizWay-Admin-Key: {{admin_key}}

HTTP 204
