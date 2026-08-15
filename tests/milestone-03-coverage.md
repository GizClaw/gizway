# Milestone 03 Test Coverage

This file records the blocking contract coverage reviewed before deleting the
superseded contract tests.

| Contract area | Blocking evidence |
|---|---|
| One empty-database schema per service | `data/sql/*/milestone03_contract_test.go`, PostgreSQL schema tests |
| Initialize, default Merchant, PAYG Product/Subscription, Key and Top-up | Hurl stories 01-05, including published visibility/subscription and owner-only Product mutation; PostgreSQL uniqueness/FK/check/trigger tests |
| Credit Check ownership snapshot and 300-second default | Hurl story 05 and generated OpenAPI contract |
| Subscription Key plaintext, HMAC privacy and immutability | Hurl story 03, PostgreSQL trigger test, PowerSync schema/authorization tests |
| GizWay command-only user API and no Admin API | Hurl story 06, OpenAPI inventory and route-boundary tests |
| Atomic Bifrost Key, Merchant binding and procurement prices | adapter transaction contract, PostgreSQL unique constraints, command E2E |
| Provider Key price dimension and lowest-price routing | PostgreSQL contract, metric-completeness/comparator unit tests, and Provider Key command E2E |
| OpenAI, Anthropic and Gemini official Go SDKs | pinned `tests/sdk` module: non-streaming, streaming, parameter forwarding, and per-protocol failure matrices |
| Billing closure and failure safety | every successful SDK call checks selected Provider Key/Log, AI Order, reported Outbox, Charge, Commission, Ledger, and exactly one Provider request; revoked Key, inactive Model, and Provider failure remain unbilled |
| Charge boundaries | Hurl verifies negative-balance posting plus subsequent Credit Check denial, and permits only revoked-Key calls whose service start is at or before `revoked_at` |
| Bifrost execution logs | SQL and SDK assertions require success/error status, Provider/Model/Key, tokens, latency, execution mode, and zero-price/failure logging |
| Zero-price local execution | SDK response plus Bifrost Log presence and direct AI Order/Outbox/Charge absence assertions |
| Realtime | Hurl drives a valid WebSocket through Provider events, Usage and Charge; unit coverage expires unused sessions |
| Process-only health | route unit test and Hurl story 07 |
| Two local databases and three PowerSync services | PowerSync schema, dual-database, GizPay/GizWay and CN/Global cases |
| JWT and owner isolation | signed issuer/subject Sync Streams, two-user and invalid-audience cases |
| Offline lifecycle and upload mapping | disconnect/reconnect watch, logout cleanup, retry/rejection queue tests |
| Complete current HTTP inventory | OpenAPI-to-Hurl operation inventory and stories 01-08 |

Validated dependency matrix: OpenAI Go SDK `v3.50.0`, Anthropic Go SDK
`v1.63.1`, Google Gen AI Go SDK `v1.68.0`, PowerSync Service `1.23.3`, and
`@powersync/node` `0.21.0`.

The review found and closed these test-design gaps before implementation:

1. Subscription reassignment now uses a valid second Subscription, so the
   immutability trigger—not an unrelated foreign-key failure—must reject it.
2. PowerSync upload processes one CRUD operation per batch so rejecting one
   invalid mutation cannot discard later valid mutations.
3. SDK tests explicitly cover provider failure, invalid authentication/model,
   streaming single-charge behavior, CN smoke, and zero-price financial-row
   absence.
4. PowerSync tests explicitly cover invalid audience, offline reads, reconnect
   updates, two-user Key isolation, regional isolation, and local database
   cleanup.
5. Provider Keys missing any customer billing metric are excluded from routing,
   and price updates reject models outside the Key's active Provider.
6. Default Merchant display names honor signed `name`/`preferred_username`
   claims; non-default Merchants support soft disable while the fixed default
   Merchant is protected.
7. PowerSync schemas retain the default-Merchant marker, public AI Order timing
   and routing fields, and the user-visible Charge order snapshot.
8. Undeclared HTTP methods cannot invoke command handlers, including the
   Subscription Key revoke route.
9. PowerSync live acceptance creates billed calls in both regions before sync,
   then requires non-empty owner AI Orders/Usage, Charge snapshots, two-user
   isolation, and disjoint CN/Global Usage model IDs.
10. GizWay Sync Streams use explicit `gizway` schema qualification, and E2E
    cleanup traps preserve the original failure status instead of reporting a
    failed runner as PASS.
11. Posted Ledger Entry reassignment validates both the old and new
    Transaction IDs, preventing an update from unbalancing the source
    Transaction.
12. Provider Key creation requires explicit `status: active`; missing status is
    rejected by both OpenAPI and the command handler.
13. OpenAI `max_tokens` and `stream_options`, Anthropic `anthropic-version`, and
    Gemini generation parameters are asserted at the fake Provider boundary.

Live SDK and PowerSync cases intentionally skip when the Compose-provided M03
environment is absent. The final acceptance gate runs them with no skips.
