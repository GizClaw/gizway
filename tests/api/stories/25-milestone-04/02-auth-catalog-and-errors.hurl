# M04 deleted initializer, signed webhook boundary, Catalog Token and errors.
# covers: gizpay-webhooks.yaml#initializeHumanFromZitadel
# covers: gizway-public.yaml#getPublicCatalogToken
# covers: gizway-public.yaml#getPublicRuntimeConfig

POST {{pay_url}}/account/v1/initialize
Authorization: Bearer {{human_token}}

HTTP 404
[Asserts]
header "Content-Type" startsWith "application/json"
jsonpath "$.error.code" == "not_found"


POST {{pay_internal_url}}/webhooks/v1/zitadel/user-authenticated
Content-Type: application/json
{
  "user": {
    "id": "unsigned-human",
    "human": {}
  },
  "userinfo": {
    "email": "unsigned@example.test",
    "name": "Unsigned"
  }
}

HTTP 401
[Asserts]
header "Content-Type" startsWith "application/json"
jsonpath "$.error.code" == "invalid_webhook_signature"


GET {{way_url}}/auth/catalog-token

HTTP 200
[Asserts]
jsonpath "$.access_token" exists
jsonpath "$.token_type" == "Bearer"
jsonpath "$.expires_at" exists


GET {{way_url}}/auth/runtime-config

HTTP 200
[Asserts]
jsonpath "$.site.hostname" exists
jsonpath "$.identity.issuer" exists
jsonpath "$.identity.audience" exists
jsonpath "$.identity.client_id" not exists
jsonpath "$.identity.redirect_uri" not exists
jsonpath "$.identity.post_logout_redirect_uri" not exists
jsonpath "$.services.gizpay_powersync_url" exists
jsonpath "$.services.gizway_powersync_url" exists


POST {{pay_url}}/account/v1/subscriptions/{{seeded_subscription_id}}/keys
Authorization: Bearer {{human_token}}
Content-Type: application/json
{
  "id": "skey_m04_blank_name",
  "name": "   "
}

HTTP 400
[Asserts]
header "Content-Type" startsWith "application/json"
jsonpath "$.error.code" == "invalid_request"
