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
