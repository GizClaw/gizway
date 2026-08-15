# M03 idempotent user initialization and pure Account queries.
# covers: account.yaml#initializeUser
# covers: account.yaml#listAccounts account.yaml#getAccountBalance
# covers: account.yaml#listAccountTransactions account.yaml#listAccountCharges

POST {{pay_url}}/account/v1/initialize
Authorization: Bearer {{human_token}}

HTTP 200
[Captures]
user_id: jsonpath "$.user_id"
account_id: jsonpath "$.account_id"
ledger_account_id: jsonpath "$.ledger_account_id"
default_merchant_id: jsonpath "$.default_merchant_id"
[Asserts]
jsonpath "$.user_id" exists
jsonpath "$.account_id" exists
jsonpath "$.ledger_account_id" exists
jsonpath "$.default_merchant_id" exists


# A retry returns the exact same aggregate and never creates duplicates.
POST {{pay_url}}/account/v1/initialize
Authorization: Bearer {{human_token}}

HTTP 200
[Asserts]
jsonpath "$.user_id" == "{{user_id}}"
jsonpath "$.account_id" == "{{account_id}}"
jsonpath "$.ledger_account_id" == "{{ledger_account_id}}"
jsonpath "$.default_merchant_id" == "{{default_merchant_id}}"


# The initializer does not accept caller-selected IDs or any request body.
POST {{pay_url}}/account/v1/initialize
Authorization: Bearer {{human_token}}
Content-Type: application/json
{"merchant_id":"attacker-selected"}

HTTP 400
[Asserts]
jsonpath "$.error.code" == "invalid_request"


GET {{pay_url}}/account/v1/accounts
Authorization: Bearer {{human_token}}

HTTP 200
[Asserts]
jsonpath "$.data" count == 1
jsonpath "$.data[0].id" == "{{account_id}}"


GET {{pay_url}}/account/v1/accounts/{{account_id}}/balance
Authorization: Bearer {{human_token}}

HTTP 200
[Asserts]
jsonpath "$.account_id" == "{{account_id}}"
jsonpath "$.balance_microcredits" exists


GET {{pay_url}}/account/v1/accounts/{{account_id}}/transactions
Authorization: Bearer {{human_token}}

HTTP 200
[Asserts]
jsonpath "$.data" exists


GET {{pay_url}}/account/v1/accounts/{{account_id}}/charges
Authorization: Bearer {{human_token}}

HTTP 200
[Asserts]
jsonpath "$.data" exists


# Account ownership is non-enumerating.
GET {{pay_url}}/account/v1/accounts/{{account_id}}/balance
Authorization: Bearer {{human_token_two}}

HTTP 404
[Asserts]
jsonpath "$.error.code" == "not_found"
