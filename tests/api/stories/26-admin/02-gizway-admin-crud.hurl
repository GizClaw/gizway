# GizWay Admin CRUD over one regional service.
# covers: gizway-admin.yaml#createAdminProvider gizway-admin.yaml#listAdminProviders gizway-admin.yaml#getAdminProvider
# covers: gizway-admin.yaml#updateAdminProvider gizway-admin.yaml#deleteAdminProvider
# covers: gizway-admin.yaml#createAdminModel gizway-admin.yaml#listAdminModels gizway-admin.yaml#getAdminModel
# covers: gizway-admin.yaml#updateAdminModel gizway-admin.yaml#deleteAdminModel
# covers: gizway-admin.yaml#getAdminModelCustomerPrices gizway-admin.yaml#replaceAdminModelCustomerPrices
# covers: gizway-admin.yaml#createAdminModelListing gizway-admin.yaml#listAdminModelListings gizway-admin.yaml#getAdminModelListing
# covers: gizway-admin.yaml#updateAdminModelListing gizway-admin.yaml#deleteAdminModelListing
# covers: gizway-admin.yaml#createAdminProviderKey gizway-admin.yaml#listAdminProviderKeys gizway-admin.yaml#getAdminProviderKey
# covers: gizway-admin.yaml#updateAdminProviderKey gizway-admin.yaml#deleteAdminProviderKey gizway-admin.yaml#rotateAdminProviderKeySecret
# covers: gizway-admin.yaml#getAdminProviderKeyPrices gizway-admin.yaml#replaceAdminProviderKeyPrices

POST {{way_url}}/admin/v1/providers
X-GizWay-Admin-Key: {{admin_key}}
Content-Type: application/json
{
  "id": "provider_admin_hurl",
  "name": "Admin Hurl Provider",
  "kind": "openai",
  "base_url": "http://fake-provider-cn:19000",
  "status": "active"
}

HTTP 201
[Asserts]
jsonpath "$.id" == "provider_admin_hurl"


GET {{way_url}}/admin/v1/providers
X-GizWay-Admin-Key: {{admin_key}}

HTTP 200
[Asserts]
jsonpath "$.data[?(@.id == 'provider_admin_hurl')].name" contains "Admin Hurl Provider"


GET {{way_url}}/admin/v1/providers/provider_admin_hurl
X-GizWay-Admin-Key: {{admin_key}}

HTTP 200
[Asserts]
jsonpath "$.kind" == "openai"


PATCH {{way_url}}/admin/v1/providers/provider_admin_hurl
X-GizWay-Admin-Key: {{admin_key}}
Content-Type: application/json
{
  "name": "Admin Hurl Provider Updated"
}

HTTP 200
[Asserts]
jsonpath "$.name" == "Admin Hurl Provider Updated"


POST {{way_url}}/admin/v1/models
X-GizWay-Admin-Key: {{admin_key}}
Content-Type: application/json
{
  "id": "model_admin_hurl",
  "provider_id": "provider_admin_hurl",
  "name": "admin-hurl-model",
  "provider_model": "fake-text-v1",
  "status": "active"
}

HTTP 201
[Asserts]
jsonpath "$.id" == "model_admin_hurl"


GET {{way_url}}/admin/v1/models
X-GizWay-Admin-Key: {{admin_key}}

HTTP 200
[Asserts]
jsonpath "$.data[?(@.id == 'model_admin_hurl')].provider_id" contains "provider_admin_hurl"


GET {{way_url}}/admin/v1/models/model_admin_hurl
X-GizWay-Admin-Key: {{admin_key}}

HTTP 200
[Asserts]
jsonpath "$.name" == "admin-hurl-model"


PATCH {{way_url}}/admin/v1/models/model_admin_hurl
X-GizWay-Admin-Key: {{admin_key}}
Content-Type: application/json
{
  "name": "admin-hurl-model-updated"
}

HTTP 200
[Asserts]
jsonpath "$.name" == "admin-hurl-model-updated"


PUT {{way_url}}/admin/v1/models/model_admin_hurl/customer-prices
X-GizWay-Admin-Key: {{admin_key}}
Content-Type: application/json
{
  "prices": [
    {"metric": "input_tokens", "unit_size": 1000000, "price_microcredits": 1000},
    {"metric": "output_tokens", "unit_size": 1000000, "price_microcredits": 2000}
  ]
}

HTTP 200
[Asserts]
jsonpath "$.prices" count == 2


GET {{way_url}}/admin/v1/models/model_admin_hurl/customer-prices
X-GizWay-Admin-Key: {{admin_key}}

HTTP 200
[Asserts]
jsonpath "$.prices" count == 2


POST {{way_url}}/admin/v1/model-listings
X-GizWay-Admin-Key: {{admin_key}}
Content-Type: application/json
{
  "id": "model_listing_admin_hurl",
  "model_id": "model_admin_hurl",
  "title": "Admin Hurl Model",
  "description": "Admin model listing",
  "family": "Text",
  "context": "128K",
  "latency": "Fast",
  "accent": "emerald",
  "featured": true,
  "display_order": 10,
  "availability": "available"
}

HTTP 201
[Asserts]
jsonpath "$.id" == "model_listing_admin_hurl"


GET {{way_url}}/admin/v1/model-listings
X-GizWay-Admin-Key: {{admin_key}}

HTTP 200
[Asserts]
jsonpath "$.data[?(@.id == 'model_listing_admin_hurl')].model_id" contains "model_admin_hurl"


GET {{way_url}}/admin/v1/model-listings/model_listing_admin_hurl
X-GizWay-Admin-Key: {{admin_key}}

HTTP 200
[Asserts]
jsonpath "$.availability" == "available"


PATCH {{way_url}}/admin/v1/model-listings/model_listing_admin_hurl
X-GizWay-Admin-Key: {{admin_key}}
Content-Type: application/json
{
  "title": "Admin Hurl Model Updated",
  "featured": false
}

HTTP 200
[Asserts]
jsonpath "$.title" == "Admin Hurl Model Updated"
jsonpath "$.featured" == false


POST {{way_url}}/admin/v1/provider-keys
X-GizWay-Admin-Key: {{admin_key}}
Content-Type: application/json
{
  "id": "provider_key_admin_hurl",
  "provider_id": "provider_admin_hurl",
  "owner_identity_issuer": "http://identity.e2e.gizway.test:18080",
  "owner_identity_subject": "{{human_subject}}",
  "merchant_id": "{{default_merchant_id}}",
  "name": "Admin Hurl Key",
  "key": "hurl-admin-secret",
  "status": "active",
  "prices": [
    {"model_id": "model_admin_hurl", "metric": "input_tokens", "unit_size": 1000000, "microcredits_per_unit": 500},
    {"model_id": "model_admin_hurl", "metric": "output_tokens", "unit_size": 1000000, "microcredits_per_unit": 700}
  ]
}

HTTP 201
[Asserts]
jsonpath "$.id" == "provider_key_admin_hurl"
jsonpath "$.secret_configured" == true
body not contains "hurl-admin-secret"


GET {{way_url}}/admin/v1/provider-keys
X-GizWay-Admin-Key: {{admin_key}}

HTTP 200
[Asserts]
jsonpath "$.data[?(@.id == 'provider_key_admin_hurl')].name" contains "Admin Hurl Key"
body not contains "hurl-admin-secret"


GET {{way_url}}/admin/v1/provider-keys/provider_key_admin_hurl
X-GizWay-Admin-Key: {{admin_key}}

HTTP 200
[Asserts]
jsonpath "$.provider_id" == "provider_admin_hurl"
body not contains "hurl-admin-secret"


PATCH {{way_url}}/admin/v1/provider-keys/provider_key_admin_hurl
X-GizWay-Admin-Key: {{admin_key}}
Content-Type: application/json
{
  "name": "Admin Hurl Key Updated",
  "status": "active"
}

HTTP 200
[Asserts]
jsonpath "$.name" == "Admin Hurl Key Updated"


POST {{way_url}}/admin/v1/provider-keys/provider_key_admin_hurl/rotate-secret
X-GizWay-Admin-Key: {{admin_key}}
Content-Type: application/json
{
  "key": "hurl-admin-secret-rotated"
}

HTTP 200
[Asserts]
jsonpath "$.secret_configured" == true
body not contains "hurl-admin-secret-rotated"


GET {{way_url}}/admin/v1/provider-keys/provider_key_admin_hurl/prices
X-GizWay-Admin-Key: {{admin_key}}

HTTP 200
[Asserts]
jsonpath "$.prices" count == 2


PUT {{way_url}}/admin/v1/provider-keys/provider_key_admin_hurl/prices
X-GizWay-Admin-Key: {{admin_key}}
Content-Type: application/json
{
  "prices": [
    {"model_id": "model_admin_hurl", "metric": "input_tokens", "unit_size": 1000000, "microcredits_per_unit": 600},
    {"model_id": "model_admin_hurl", "metric": "output_tokens", "unit_size": 1000000, "microcredits_per_unit": 800}
  ]
}

HTTP 200
[Asserts]
jsonpath "$.prices[0].microcredits_per_unit" == 600


DELETE {{way_url}}/admin/v1/provider-keys/provider_key_admin_hurl
X-GizWay-Admin-Key: {{admin_key}}

HTTP 204


DELETE {{way_url}}/admin/v1/model-listings/model_listing_admin_hurl
X-GizWay-Admin-Key: {{admin_key}}

HTTP 204


DELETE {{way_url}}/admin/v1/models/model_admin_hurl
X-GizWay-Admin-Key: {{admin_key}}

HTTP 204


DELETE {{way_url}}/admin/v1/providers/provider_admin_hurl
X-GizWay-Admin-Key: {{admin_key}}

HTTP 204
