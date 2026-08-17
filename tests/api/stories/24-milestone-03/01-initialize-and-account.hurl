# Retained M03 pure Account query regressions after M04 moved initialization
# to the signed ZITADEL webhook.
# covers: account.yaml#listAccounts account.yaml#getAccountBalance
# covers: account.yaml#listAccountTransactions account.yaml#listAccountCharges


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
