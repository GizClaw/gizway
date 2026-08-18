# Executable API stories

The Hurl files below `stories/24-milestone-03/` are the current durable API
specifications. The suite starts fresh GizPay and regional PostgreSQL databases,
embedded Bifrost stores, deterministic AI providers, and ZITADEL identities.

`stories/26-admin/` is the black-box TDD contract for the Admin Key-only CRUD
surfaces. The disposable business Seed uses those generated Admin clients and
the existing Account API; it does not prepare business state with SQL.

Run the black-box API suite from the repository root:

```sh
make test-unit-api
```

Run one Admin story with `MILESTONE_STORY_FILTER=01-gizpay-admin-crud make
test-unit-api` (or the corresponding story basename).

Run the complete Milestone 03 acceptance matrix with:

```sh
make test-e2e
```

The top-level E2E gate independently records API, official SDK, and PowerSync
results. Go tests remain responsible for internal properties such as transaction
rollback, malformed dependencies, cancellation, concurrency, and lifecycle.

When adding a Gizway-owned OpenAPI operation, add its `operationId` to the
owning Hurl file's `# covers:` declaration. Provider-compatible wire cases use
`# protocol covers:` comments.
