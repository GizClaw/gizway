# Executable API stories

The Hurl files below `stories/24-milestone-03/` are the current durable API
specifications. The suite starts fresh GizPay and regional PostgreSQL databases,
embedded Bifrost stores, deterministic AI providers, and ZITADEL identities.

Run the black-box API suite from the repository root:

```sh
make test-unit-api
```

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
