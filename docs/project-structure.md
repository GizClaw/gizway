# Project structure

Gizway is a Go service with provider-compatible AI protocols and Gizway-owned
Account, Pay, and Admin APIs. Package boundaries follow runtime ownership rather
than protocol names alone.

```text
gizway/
├── .github/workflows/     # CI and reusable Codex review entrypoints
├── Makefile               # Canonical local and CI quality-gate commands
├── scripts/
│   ├── lint/              # Language-oriented static-analysis entrypoints
│   └── test-unit/         # Local test runners named after Make targets
├── api/openapi/          # Gizway-owned wire contracts
├── cmd/gizway/           # Minimal executable entrypoint
├── data/
│   └── sql/
│       ├── migrations/   # Canonical PostgreSQL schema
│       └── seeds/        # Explicit non-production development fixtures
├── internal/
│   ├── app/              # Process composition and lifecycle
│   ├── api/              # HTTP routing, authentication, codecs, handlers
│   ├── service/          # State machines and transaction boundaries
│   ├── adapter/          # Bifrost, payment, risk, webhook external boundaries
│   ├── storage/          # Direct sqlx PostgreSQL connection and schema setup
│   └── store/            # SQLx queries and transactional persistence
└── tests/api/            # Hurl data and black-box acceptance contracts
```

## Dependency direction

```text
cmd -> app -> api/service -> store -> storage -> sqlx -> PostgreSQL
                         -> adapter -> external/fake dependency
```

- `cmd` parses process flags and owns no application behavior.
- `app` wires dependencies and owns startup and shutdown.
- `api` owns HTTP behavior but not SQL.
- `api` owns credential parsing, verification, and scope decisions; `store`
  owns persisted session and key state transitions.
- `service` owns state machines, idempotency, audit, and transaction boundaries.
- `adapter` owns Bifrost and other external protocols but never posts ledger entries.
- `store` owns SQL queries and transactions but not database construction.
- `storage` owns the direct `sqlx` PostgreSQL connection and schema bootstrap.
- `data/sql` is the SQL schema and fixture source of truth.
- `scripts` owns executable local and CI automation; `tests` contains test
  inputs and contracts rather than runner scripts.
- `Makefile` is the command source of truth shared by local development and CI;
  workflows compose its focused formatting, lint, build, and test targets.

Business packages such as payment settlement and ledger posting live under
`internal/service/<domain>`. Empty domain layers and one-method interfaces are
not created in advance. Externally visible behavior is specified only by the
executable Hurl stories under `tests/api/stories/`.

## Test ownership

- Hurl is the sole source of truth for externally visible business behavior.
- Go HTTP integration tests run real handlers against isolated schemas in a
  disposable Docker PostgreSQL instance.
- Store tests own transaction, concurrency, and database error behavior.
- OpenAPI linting checks the published schema independently from implementation.
