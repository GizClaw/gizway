# Project structure

All development milestones follow the repository-wide
[development-stage breaking refactor policy](./development-stage-breaking-refactors.md): there is no production
data migration or legacy compatibility surface, and each milestone leaves one current Schema, API, Config, and
test contract.

Milestone 03 is implemented as two Go processes plus three PowerSync services:

```text
cmd/gizpay     -> internal/app -> internal/gizpay -> GizPay PostgreSQL -> GizPay PowerSync
cmd/gizway     -> internal/app -> internal/gizway -> regional PostgreSQL -> regional PowerSync
                                            |----> embedded Bifrost
                                            |----> central GizPay APIs
```

- `api/openapi`: the four Milestone 03 wire contracts.
- `data/sql/gizpay`: central identity, Account, Merchant, Product,
  Subscription, Key, Top-up, Charge, Commission, and ledger schema.
- `data/sql/gizway`: regional Model, Provider Key billing/prices, AI Order,
  Usage, and Charge Outbox schema.
- `internal/adapter/bifrost`: embedded Bifrost execution and transactional
  Config Store integration.
- `internal/identity`: ZITADEL OIDC, JWKS, Private Key JWT, and project-scoped
  role verification.
- `tests/api/stories/24-milestone-03`: current API contract stories.
- `tests/sdk`: pinned official OpenAI, Anthropic, and Gemini SDK acceptance.
- `tests/powersync`: independent GizPay and regional PowerSync client contracts.
- `tests/e2e`: disposable ZITADEL, PostgreSQL, GizPay, CN/Global GizWay,
  PowerSync, and Fake Provider composition.

The implemented Milestone 03 contract is represented by the OpenAPI documents,
the current PostgreSQL schemas, and the API, SDK, PowerSync, and E2E acceptance
tests listed above.
