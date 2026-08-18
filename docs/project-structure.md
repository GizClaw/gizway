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

- `api/openapi`: seven root wire contracts, including separate GizPay and
  GizWay Admin surfaces authenticated only by the configured initial Admin Key.
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

Fixed E2E business resources are declared in
`tests/e2e/config/business-seed.yaml` and applied through generated Admin HTTP
clients. Account, Subscription, Subscription Key, and Top-up setup continues
through the public Account API. Seed code must not connect to business
PostgreSQL or call Bifrost stores directly.

The implemented Milestone 03 contract is represented by the OpenAPI documents,
the current PostgreSQL schemas, and the API, SDK, PowerSync, and E2E acceptance
tests listed above.

## Database migration ownership

Each Go image exposes a one-shot migration command which must complete before
its matching long-running service starts:

```sh
gizpay --config=/config/gizpay.yaml --migrate-only
gizway --config=/config/gizway-cn.yaml --migrate-only
gizway --config=/config/gizway-global.yaml --migrate-only
```

GizPay migrates only its service schema and atomically ensures the fixed
platform ledger principals. GizWay migrates only the selected regional service
schema; the CN and Global invocations use independent configs and databases.
The command never applies business Seed data. ZITADEL, PowerSync, and embedded
Bifrost Config/Log Store schemas retain their own lifecycle; in particular,
Bifrost schemas are still initialized when the long-running GizWay process
opens those stores.

## Release ownership

This repository owns three first-party `linux/amd64` production images:

- `ghcr.io/idy/gizway-gizpay` for the central GizPay process;
- `ghcr.io/idy/gizway-gateway` for either regional GizWay process;
- `ghcr.io/idy/gizway-web` for the vinext standalone server.

A strict SemVer Git tag runs the existing CI at that exact commit, builds and
smokes OCI layouts once, publishes immutable version and full-revision tags to
public GHCR packages, and keyless-signs the resulting digests with the GitHub
Actions OIDC identity. The repository never stores signing keys or publishes a
mutable `latest` tag.

`GizClaw/deploy` owns production configuration, Secret delivery, signature
verification, digest pinning, rollout, and rollback. Nothing in this release
workflow contacts or mutates a production environment.
