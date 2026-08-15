# M03 Merchant, PAYG Product, and Subscription lifecycle.
# covers: account.yaml#listMerchants account.yaml#createMerchant account.yaml#getMerchant account.yaml#updateMerchant
# covers: account.yaml#listProducts account.yaml#listMerchantProducts account.yaml#createMerchantProduct
# covers: account.yaml#getProduct account.yaml#updateProduct account.yaml#createProductSubscription
# covers: account.yaml#listSubscriptions account.yaml#getSubscription account.yaml#updateSubscription

POST {{pay_url}}/account/v1/initialize
Authorization: Bearer {{human_token}}

HTTP 200
[Captures]
account_id: jsonpath "$.account_id"
default_merchant_id: jsonpath "$.default_merchant_id"


POST {{pay_url}}/account/v1/initialize
Authorization: Bearer {{human_token_two}}

HTTP 200
[Captures]
account_id_two: jsonpath "$.account_id"


GET {{pay_url}}/account/v1/merchants
Authorization: Bearer {{human_token}}

HTTP 200
[Asserts]
jsonpath "$.data[?(@.id == '{{default_merchant_id}}')]" count == 1


GET {{pay_url}}/account/v1/merchants/{{default_merchant_id}}
Authorization: Bearer {{human_token}}

HTTP 200
[Asserts]
jsonpath "$.is_default" == true


POST {{pay_url}}/account/v1/merchants
Authorization: Bearer {{human_token}}
Content-Type: application/json
{"settlement_account_id":"{{account_id}}","legal_name":"M03 Merchant LLC","public_name":"M03 Merchant"}

HTTP 201
[Captures]
merchant_id: jsonpath "$.id"
[Asserts]
jsonpath "$.is_default" == false


GET {{pay_url}}/account/v1/merchants/{{merchant_id}}
Authorization: Bearer {{human_token}}

HTTP 200
[Asserts]
jsonpath "$.public_name" == "M03 Merchant"


PATCH {{pay_url}}/account/v1/merchants/{{merchant_id}}
Authorization: Bearer {{human_token}}
Content-Type: application/json
{"public_name":"M03 Merchant Updated"}

HTTP 200
[Asserts]
jsonpath "$.public_name" == "M03 Merchant Updated"


POST {{pay_url}}/account/v1/merchants/{{merchant_id}}/products
Authorization: Bearer {{human_token}}
Content-Type: application/json
{"name":"M03 PAYG","billing_mode":"pay_as_you_go","terms_version":"m03-v1"}

HTTP 201
[Captures]
product_id: jsonpath "$.id"
[Asserts]
jsonpath "$.billing_mode" == "pay_as_you_go"


GET {{pay_url}}/account/v1/merchants/{{merchant_id}}/products
Authorization: Bearer {{human_token}}

HTTP 200
[Asserts]
jsonpath "$.data[?(@.id == '{{product_id}}')]" count == 1


GET {{pay_url}}/account/v1/products
Authorization: Bearer {{human_token}}

HTTP 200
[Asserts]
jsonpath "$.data[?(@.id == '{{product_id}}')]" count == 1


GET {{pay_url}}/account/v1/products/{{product_id}}
Authorization: Bearer {{human_token}}

HTTP 200
[Asserts]
jsonpath "$.id" == "{{product_id}}"


# Any authenticated user who can see the published Product can read and subscribe to it.
GET {{pay_url}}/account/v1/products/{{product_id}}
Authorization: Bearer {{human_token_two}}

HTTP 200
[Asserts]
jsonpath "$.id" == "{{product_id}}"


POST {{pay_url}}/account/v1/products/{{product_id}}/subscriptions
Authorization: Bearer {{human_token_two}}
Content-Type: application/json
{"account_id":"{{account_id_two}}","terms_version":"m03-v1"}

HTTP 201
[Asserts]
jsonpath "$.product_id" == "{{product_id}}"


# An unpublished Product is neither visible nor subscribable.
GET {{pay_url}}/account/v1/products/{{hidden_product_id}}
Authorization: Bearer {{human_token_two}}

HTTP 404


POST {{pay_url}}/account/v1/products/{{hidden_product_id}}/subscriptions
Authorization: Bearer {{human_token_two}}
Content-Type: application/json
{"account_id":"{{account_id_two}}","terms_version":"m03-v1"}

HTTP 404


# Product mutation remains owner-only even though reads and subscriptions are public.
PATCH {{pay_url}}/account/v1/products/{{product_id}}
Authorization: Bearer {{human_token_two}}
Content-Type: application/json
{"name":"Must Not Be Applied"}

HTTP 404


PATCH {{pay_url}}/account/v1/products/{{product_id}}
Authorization: Bearer {{human_token}}
Content-Type: application/json
{"name":"M03 PAYG Updated"}

HTTP 200
[Asserts]
jsonpath "$.name" == "M03 PAYG Updated"


POST {{pay_url}}/account/v1/products/{{product_id}}/subscriptions
Authorization: Bearer {{human_token}}
Content-Type: application/json
{"account_id":"{{account_id}}","terms_version":"m03-v1"}

HTTP 201
[Captures]
subscription_id: jsonpath "$.id"
[Asserts]
jsonpath "$.product_id" == "{{product_id}}"


GET {{pay_url}}/account/v1/subscriptions
Authorization: Bearer {{human_token}}

HTTP 200
[Asserts]
jsonpath "$.data[?(@.id == '{{subscription_id}}')]" count == 1


GET {{pay_url}}/account/v1/subscriptions/{{subscription_id}}
Authorization: Bearer {{human_token}}

HTTP 200
[Asserts]
jsonpath "$.id" == "{{subscription_id}}"


PATCH {{pay_url}}/account/v1/subscriptions/{{subscription_id}}
Authorization: Bearer {{human_token}}
Content-Type: application/json
{"status":"inactive"}

HTTP 200
[Asserts]
jsonpath "$.status" == "inactive"


# A non-default Merchant can be soft-disabled.
PATCH {{pay_url}}/account/v1/merchants/{{merchant_id}}
Authorization: Bearer {{human_token}}
Content-Type: application/json
{"status":"inactive"}

HTTP 200
[Asserts]
jsonpath "$.status" == "inactive"


# The fixed default Merchant cannot be disabled.
PATCH {{pay_url}}/account/v1/merchants/{{default_merchant_id}}
Authorization: Bearer {{human_token}}
Content-Type: application/json
{"status":"inactive"}

HTTP 409
[Asserts]
jsonpath "$.error.code" == "default_merchant_in_use"
