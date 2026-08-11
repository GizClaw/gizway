# Executable API stories

The Hurl files below `stories/` are the durable business specifications. Each
file is self-contained, heavily commented, and starts from a fresh isolated
PostgreSQL schema plus fresh external AI and payment fixture processes.
Executable runners live under `scripts/test-unit/`; this directory contains
only test contracts, fixtures, and their documentation.

Run the complete black-box suite from the repository root:

```sh
make test-unit-api
```

Useful quality gates:

```sh
make fmt-hurl
make test-unit-go
make test-unit-go-race
```

The top-level `make test-unit` target runs every local test without contacting
real external services. This includes the fake-provider API stories and the
PostgreSQL companion suite. The runner starts a disposable Docker PostgreSQL
instance when `GIZWAY_TEST_POSTGRES_DSN` is absent. Future tests against real deployed dependencies
will live under a separate `test-e2e` target.

GitHub Actions composes the focused Make targets directly so each failed gate is
reported as its own workflow step.

Go tests are reserved for internal quality properties that Hurl cannot observe
directly, including SQL transaction rollback, malformed dependency responses,
WebSocket frame relay, cancellation, concurrency and process lifecycle.

When adding a Gizway-owned OpenAPI operation, add its `operationId` to the
owning Hurl file's `# covers:` declaration. Provider-owned OpenAI wire cases use
`# protocol covers:` comments instead of copying the upstream schema.
