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

For the disposable Compose E2E environment, the caller supplies a currently valid certificate/key pair whose SANs cover the Global, CN, Identity, and Pay test hosts. CI creates a one-day fixture outside every image; local callers provide an equivalent fixture. The E2E runner verifies expiry, SAN coverage, and the key pair before startup, trusts only that certificate's SPKI for browser tests, and proves that an unknown SNI is rejected. These fixtures are test inputs, not product resources or production deployment configuration.

The public AI surface is intentionally protocol-specific: `/openai/v1/...`, `/anthropic/v1/...`, and `/genai/v1beta/...`. Root `/v1`, root `/v1beta`, and Bifrost aggregate APIs are not exposed.
