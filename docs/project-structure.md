# Project structure

GizWay has two independent Go binaries and seven release images:

```text
cmd/gizpay -> GizPay database -> Pay PowerSync
cmd/gizway -> Global or CN database -> regional PowerSync -> embedded Bifrost
init-only config -> service role + source replication role + dedicated PowerSync storage owner

Traefik entry -> protocol-prefixed GizWay API / GizPay / PowerSync / static Caddy Web
Central entry -> ZITADEL API / ZITADEL Login
```

`gizpay` and `gizway` each provide `init` and `serve`; they are never combined into one binary or one image. The same `gizway` digest runs Global and CN with isolated configurations and databases.

`tests/e2e/compose.yaml` is the executable local/integration reference for image, network, configuration, dependency, and initialization boundaries. `tests/e2e/config/resources.yaml` declares fixed test resources; `tests/e2e/resources` creates them only through ZITADEL, GizPay, and GizWay APIs. SQL under `tests/e2e/sql` asserts schema contracts and does not seed product data.

The default production-facing names remain `gateway.gizclaw.com` and `auth.gizclaw.com`; CN and Pay hosts may remain runtime inputs under `*.gizclaw.com`. Entry also accepts only the fixed GizWay deployment aliases `global.gizway.com`, `cn.gizway.com`, and `pay.gizway.com`, not an arbitrary `*.gizway.com` wildcard. Production infrastructure, secrets, certificates, deployment manifests, rollout, and rollback are outside this repository.
