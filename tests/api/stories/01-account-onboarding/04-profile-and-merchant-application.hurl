# Story 01 — Update a profile and apply for a reviewed merchant account.
#
# Product behavior:
# - Profile changes persist and are visible on a subsequent independent read.
# - A merchant application creates an owned merchant account in pending review;
#   self-service creation can never directly approve the merchant.
# - Unknown fields and duplicate merchant applications are rejected without
#   creating extra accounts.
#
# Fixture: story-base
# Isolation: this file receives a new PostgreSQL schema and server process.
# covers: updateCurrentUser createMerchantAccount listAccounts

PATCH {{base_url}}/account/v1/me
Authorization: bearer {{active_user_one_key}}
Idempotency-Key: story-01-update-profile
Content-Type: application/json
{
  "display_name": "Updated Through Hurl"
}

HTTP 200
[Asserts]
jsonpath "$.display_name" == "Updated Through Hurl"


# Generic PATCH commands persist their raw response in the same transaction as
# the profile row and audit event. Exact retry does not run the mutation twice.
PATCH {{base_url}}/account/v1/me
Authorization: Bearer {{active_user_one_key}}
Idempotency-Key: story-01-update-profile
Content-Type: application/json
{
  "display_name": "Updated Through Hurl"
}

HTTP 200
[Asserts]
jsonpath "$.display_name" == "Updated Through Hurl"


# A changed payload under the same key is rejected before the Store handler.
PATCH {{base_url}}/account/v1/me
Authorization: Bearer {{active_user_one_key}}
Idempotency-Key: story-01-update-profile
Content-Type: application/json
{
  "display_name": "Must Not Replace The Profile"
}

HTTP 409
[Asserts]
jsonpath "$.error.code" == "idempotency_conflict"


# Read-after-write proves the update was committed to PostgreSQL.
GET {{base_url}}/account/v1/me
Authorization: Bearer {{active_user_one_key}}

HTTP 200
[Asserts]
jsonpath "$.display_name" == "Updated Through Hurl"


POST {{base_url}}/account/v1/merchant_accounts
Authorization: Bearer {{active_user_one_key}}
Idempotency-Key: story-01-create-merchant
Content-Type: application/json
{
  "name": "Story Merchant Account",
  "legal_name": "Story Merchant Pte. Ltd.",
  "public_name": "Story Merchant",
  "country_code": "SG",
  "website_url": "https://merchant.example"
}

HTTP 201
[Captures]
merchant_account_id: jsonpath "$.account.id"
[Asserts]
jsonpath "$.account.kind" == "merchant"
jsonpath "$.account.status" == "active"
jsonpath "$.merchant_status" == "pending"
jsonpath "$.review_level" == "basic"
jsonpath "$.country_code" == "SG"


# Merchant application is a credential-scoped command. Replaying the same
# key and payload returns the original account rather than treating a network
# retry as a second application.
POST {{base_url}}/account/v1/merchant_accounts
Authorization: Bearer {{active_user_one_key}}
Idempotency-Key: story-01-create-merchant
Content-Type: application/json
{
  "name": "Story Merchant Account",
  "legal_name": "Story Merchant Pte. Ltd.",
  "public_name": "Story Merchant",
  "country_code": "SG",
  "website_url": "https://merchant.example"
}

HTTP 201
[Asserts]
jsonpath "$.account.id" == "{{merchant_account_id}}"


# The new account appears in the same owner's account projection.
GET {{base_url}}/account/v1/accounts
Authorization: Bearer {{active_user_one_key}}

HTTP 200
[Asserts]
jsonpath "$.data" count == 2
jsonpath "$.data[1].id" == "{{merchant_account_id}}"
jsonpath "$.data[1].kind" == "merchant"


# A second application is not silently created. Initial launch permits one
# merchant identity per owner until an explicit multi-merchant policy exists.
POST {{base_url}}/account/v1/merchant_accounts
Authorization: Bearer {{active_user_one_key}}
Idempotency-Key: story-01-duplicate-merchant
Content-Type: application/json
{
  "name": "Another Merchant",
  "legal_name": "Another Merchant Pte. Ltd.",
  "public_name": "Another Merchant",
  "country_code": "SG",
  "website_url": null
}

HTTP 409
[Asserts]
jsonpath "$.error.code" == "conflict"
