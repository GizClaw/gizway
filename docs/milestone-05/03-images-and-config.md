# Milestone 05 images and configuration

GizWay publishes exactly seven `linux/amd64` OCI images. The machine-readable source of truth is `release/images.json`.

| Image | Runtime | Long-running instances |
| --- | --- | --- |
| `gizway-gizpay` | independent `gizpay` Go binary | Pay |
| `gizway-gateway` | independent `gizway` Go binary | Global, CN |
| `gizway-web` | Caddy static server | Global, CN |
| `gizway-entry` | Traefik with built-in route contract | Global, CN, Central |
| `gizway-zitadel` | pinned ZITADEL wrapper | Identity API |
| `gizway-zitadel-login` | pinned official Login wrapper | Login UI |
| `gizway-powersync` | pinned PowerSync wrapper with three profiles | Pay, Global, CN |

That is seven released images and thirteen long-running instances. Node is permitted in the Web builder and in upstream products that officially require it; no first-party production Node server exists. `gizway-web` contains only Caddy and static assets.

Fixed product routing and profile configuration lives in the wrapper images. Hostnames, upstream URLs, database connections, master keys, credentials, TLS certificates, and private keys are runtime inputs. Entry images never request certificates; a deployment mounts externally managed certificate files read-only.

Entry accepts the existing `*.gizclaw.com` and `*.gizclaw.test` subdomain namespaces plus the exact deployment hosts `global.gizway.com`, `cn.gizway.com`, and `pay.gizway.com`. It rejects the GizWay apex, `www`, and every other `*.gizway.com` name. Upstreams are pathless `http://` or `https://` origins, except that `ZITADEL_UPSTREAM` alone may use an authority-only `h2c://` origin for the private cleartext HTTP/2 hop. Login and PowerSync mounts match the exact base or a slash-delimited descendant; lookalike prefixes fall through to the existing lower-priority route.

Each Pay, Global, and CN PowerSync profile has the same PostgreSQL runtime contract:

| Input | Required behavior |
| --- | --- |
| `PS_SOURCE_URI` | source PostgreSQL URI without fragment or TLS query keys |
| `PS_SOURCE_SSL_MODE` | exactly `disable`, `verify-ca`, or `verify-full` |
| `PS_SOURCE_CA_FILE` | absolute readable X.509 CA bundle for either verified mode; unset for `disable` |
| `PS_STORAGE_URI` | storage PostgreSQL URI without fragment or TLS query keys |
| `PS_STORAGE_SSL_MODE` | exactly `disable`, `verify-ca`, or `verify-full` |
| `PS_STORAGE_CA_FILE` | absolute readable X.509 CA bundle for either verified mode; unset for `disable` |

The non-root wrapper validates these inputs, supplies CA bytes to PowerSync's native in-memory `cacert` fields, and then execs the inherited PowerSync command. It never copies CA material into an image or prints URIs, credentials, certificate bytes, or environment dumps. Managed deployments use `verify-full`; explicit `disable` is limited to disposable local probes.

For the disposable Compose E2E environment, the caller supplies a currently valid certificate/key pair whose SANs cover the Global, CN, Identity, and Pay test hosts. CI creates a one-day fixture outside every image; local callers provide an equivalent fixture. The E2E runner verifies expiry, SAN coverage, and the key pair before startup, trusts only that certificate's SPKI for browser tests, and proves that an unknown SNI is rejected. These fixtures are test inputs, not product resources or production deployment configuration.

The E2E runner separately creates an ephemeral PostgreSQL certificate whose SANs cover the source and storage service names. All three PowerSync profiles connect to both databases with `verify-full`; the runner verifies the resulting source and storage sessions through PostgreSQL `pg_stat_ssl` and deletes the fixture on every exit path.

The public AI surface is intentionally protocol-specific: `/openai/v1/...`, `/anthropic/v1/...`, and `/genai/v1beta/...`. Root `/v1`, root `/v1beta`, and Bifrost aggregate APIs are not exposed.
