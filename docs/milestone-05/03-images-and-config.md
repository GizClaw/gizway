# Milestone 05 images and configuration

Gizway publishes three production OCI images for `linux/amd64`:

| Process | Image | Runtime command |
| --- | --- | --- |
| GizPay | `ghcr.io/idy/gizway-gizpay` | `/usr/local/bin/gizpay` |
| Regional GizWay | `ghcr.io/idy/gizway-gateway` | `/usr/local/bin/gizway` |
| Web | `ghcr.io/idy/gizway-web` | `node dist/standalone/server.js` |

The Go and Node builder/runtime bases are pinned by manifest digest. Go
binaries are static, trimmed, and run as numeric non-root user `65532:65532`.
The Web image runs as the Node image's non-root `node` user and contains the
standalone dependency closure, not the full builder `node_modules` or developer
tools. Production Dockerfiles never reference the E2E Dockerfiles.

Release builds receive only `version`, full Git `revision`, and the commit's UTC
committer time. They become OCI labels and immutable `/healthz` fields. Local
builds return `devel`, `unknown`, and `unknown`. Runtime configuration, database
DSNs, URLs, credentials, provider keys, and Secrets remain runtime inputs and
must never be supplied as Docker build arguments or copied into image layers.

All three health endpoints are process-only probes. They return `status`,
`service`, `version`, `revision`, `build_time`, and a newly calculated UTC
`server_time`; they do not query databases or upstream services.

## One-shot migration commands

Before starting either Go service, run the matching image once with its normal
version-1 YAML file:

```sh
gizpay --config=/config/gizpay.yaml --migrate-only
gizway --config=/config/gizway-global.yaml --migrate-only
gizway --config=/config/gizway-cn.yaml --migrate-only
```

Migration mode strictly rejects unknown YAML fields but validates only
`version`, `database.dsn`, and the lowercase `database.schema`. It does not
require or read Admin, HMAC, OIDC, Provider callback, or TLS Secret files, and
it does not start HTTP, workers, upstream clients, or Bifrost stores. It is
mutually exclusive with `--initialize`, `--check-config`, and
`--print-effective-config`.

After the job exits successfully, start the long-running process with only
`--config`; deployment commands must not include `--initialize`:

```sh
gizpay --config=/config/gizpay.yaml
gizway --config=/config/gizway-global.yaml
```
