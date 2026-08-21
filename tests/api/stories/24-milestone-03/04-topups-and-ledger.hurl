# M03 Fake Channel Top-up and idempotent ledger posting.
# covers: account.yaml#listAccountTopups account.yaml#createAccountTopup

GET {{pay_url}}/account/v1/accounts/{{account_id}}/balance
Authorization: Bearer {{human_token}}

HTTP 200
[Captures]
balance_before: jsonpath "$.balance_microcredits"


POST {{pay_url}}/account/v1/accounts/{{account_id}}/topups
Authorization: Bearer {{human_token}}
Content-Type: application/json
{"id":"topup_m03_retained","amount_microcredits":100000000,"channel":"fake","external_reference":"m03-topup-001"}

HTTP 201
[Captures]
topup_id: jsonpath "$.id"
[Asserts]
jsonpath "$.account_id" == "{{account_id}}"
jsonpath "$.status" == "succeeded"
jsonpath "$.amount_microcredits" == 100000000
jsonpath "$.credited_at" exists


GET {{pay_url}}/account/v1/accounts/{{account_id}}/topups
Authorization: Bearer {{human_token}}

HTTP 200
[Asserts]
jsonpath "$.data[?(@.id == '{{topup_id}}')].id" == "{{topup_id}}"


GET {{pay_url}}/account/v1/accounts/{{account_id}}/balance
Authorization: Bearer {{human_token}}

HTTP 200
[Asserts]
jsonpath "$.balance_microcredits" > {{balance_before}}


# The same channel reference cannot post Credit twice.
POST {{pay_url}}/account/v1/accounts/{{account_id}}/topups
Authorization: Bearer {{human_token}}
Content-Type: application/json
{"id":"topup_m03_duplicate_reference","amount_microcredits":100000000,"channel":"fake","external_reference":"m03-topup-001"}

HTTP 409
[Asserts]
jsonpath "$.error.code" == "duplicate_external_reference"


POST {{pay_url}}/account/v1/accounts/{{account_id}}/topups
Authorization: Bearer {{human_token}}
Content-Type: application/json
{"id":"topup_m03_zero","amount_microcredits":0,"channel":"fake","external_reference":"m03-topup-zero"}

HTTP 400


POST {{pay_url}}/account/v1/accounts/{{account_id}}/topups
Authorization: Bearer {{human_token_two}}
Content-Type: application/json
{"id":"topup_m03_other","amount_microcredits":1,"channel":"fake","external_reference":"m03-topup-other"}

HTTP 404
