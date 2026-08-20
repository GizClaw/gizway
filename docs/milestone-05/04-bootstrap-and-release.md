# Milestone 05 initialization and release

The two business images remain independent and each exposes explicit `serve` and `init` commands:

```sh
gizpay init --config=/config/gizpay-init.yaml
gizpay serve --config=/config/gizpay.yaml
gizway init --config=/config/gizway-global-init.yaml
gizway serve --config=/config/gizway-global.yaml
```

`init` is an idempotent one-shot operation. Its strict, init-only configuration creates the service login, migrates only the selected business schema, creates the configured PowerSync replication login/publication/source grants, and creates the dedicated storage database owned by its PowerSync login. The serve configuration cannot decode or inherit any management DSN. `init` does not start HTTP and does not create products, identities, accounts, models, subscriptions, keys, top-ups, or other product resources. `serve` rejects database auto-initialization.

ZITADEL uses its official `start-from-init` contract. After services are ready, the E2E resource runner uses ZITADEL standard APIs, generated GizPay/GizWay Admin clients, and public APIs. It never connects to product databases.

Release build, verify, publish, manifest, anonymous-read, and Cosign loops all consume `release/images.json`, so the seven-package set cannot drift between workflow stages. Production deployment remains a separate repository responsibility; this repository contains no Terraform, Kubernetes, Helm, certificate automation, or Apply logic.
