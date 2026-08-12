# Story 19 — Node identity is stable; certificate rows control mTLS access.
#
# A node has no product-level disable switch. Operators register a pending
# fingerprint, activate it only while its validity interval is current, and
# revoke that individual certificate during rotation or incident response.
# covers: gizpay-admin.yaml#createGatewayNode gizpay-admin.yaml#getGatewayNode gizpay-admin.yaml#registerGatewayNodeCertificate gizpay-admin.yaml#activateGatewayNodeCertificate gizpay-admin.yaml#revokeGatewayNodeCertificate

POST {{pay_url}}/admin/v1/gateway_nodes
Authorization: Bearer {{admin_api_key}}
Idempotency-Key: story-19-create-node
Content-Type: application/json
{
  "id": "story-global-node",
  "region": "global",
  "name": "Story Global Node"
}

HTTP 201
[Asserts]
jsonpath "$.id" == "story-global-node"
jsonpath "$.region" == "global"
jsonpath "$.certificates" count == 0


POST {{pay_url}}/admin/v1/gateway_nodes/story-global-node/certificates
Authorization: Bearer {{admin_api_key}}
Idempotency-Key: story-19-register-certificate
Content-Type: application/json
{
  "fingerprint_sha256": "1111111111111111111111111111111111111111111111111111111111111111",
  "serial_number": "story-serial-one",
  "subject_dn": "CN=story-global-node",
  "san_uri": "spiffe://gizway/gateway/global/story-global-node",
  "not_before": "2026-08-01T00:00:00.000000000Z",
  "not_after": "2027-08-01T00:00:00.000000000Z"
}

HTTP 201
[Captures]
certificate_id: jsonpath "$.id"
[Asserts]
jsonpath "$.status" == "pending"
jsonpath "$.fingerprint_sha256" == "1111111111111111111111111111111111111111111111111111111111111111"


POST {{pay_url}}/admin/v1/gateway_nodes/story-global-node/certificates/{{certificate_id}}/activate
Authorization: Bearer {{admin_api_key}}
Idempotency-Key: story-19-activate-certificate

HTTP 200
[Asserts]
jsonpath "$.status" == "active"


GET {{pay_url}}/admin/v1/gateway_nodes/story-global-node
Authorization: Bearer {{admin_api_key}}

HTTP 200
[Asserts]
jsonpath "$.certificates" count == 1
jsonpath "$.certificates[0].status" == "active"


POST {{pay_url}}/admin/v1/gateway_nodes/story-global-node/certificates/{{certificate_id}}/revoke
Authorization: Bearer {{admin_api_key}}
Idempotency-Key: story-19-revoke-certificate

HTTP 200
[Asserts]
jsonpath "$.status" == "revoked"
