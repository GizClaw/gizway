# Story 22 — Internal readiness returns the identity authenticated for the
# calling node. In the fast story harness the explicit fixture identity stands
# in for TLS; the process-level E2E repeats this assertion with real mTLS.
# covers: internal-gizpay.yaml#getInternalReadiness

GET {{pay_url}}/internal/v1/readyz

HTTP 200
[Asserts]
jsonpath "$.status" == "ready"
jsonpath "$.node_id" == "gw-global-e2e"
jsonpath "$.region" == "global"
