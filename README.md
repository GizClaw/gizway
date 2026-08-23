# GizWay

[![CI](https://github.com/GizClaw/gizway/actions/workflows/ci.yml/badge.svg)](https://github.com/GizClaw/gizway/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/GizClaw/gizway?include_prereleases&sort=semver)](https://github.com/GizClaw/gizway/releases)
[![Go Version](https://img.shields.io/github/go-mod/go-version/GizClaw/gizway)](go.mod)
[![License](https://img.shields.io/badge/license-BSD--3--Clause-blue)](LICENSE)

GizWay is the AI gateway, realtime synchronization, identity, and credit
payment platform used by GizClaw products. It runs one central GizPay service
and independent regional GizWay gateways for Global and CN deployments.

The repository owns application binaries, API contracts, browser SDK sources,
runtime image composition, and development/E2E validation. Production
infrastructure, secrets, rollout, rollback, and deployment are owned by the
separate `GizClaw/deploy` repository.

## Components

- `cmd/gizpay`: central account, product, subscription, credit, and payment
  service.
- `cmd/gizway`: regional AI gateway backed by an independent PostgreSQL
  database and embedded Bifrost provider runtime.
- `api/openapi`: seven source OpenAPI 3.1 contracts and generated Go bindings.
- `sdk/web`: browser SDK published as `@gizclaw/gizway-browser-sdk`.
- `docker`: production image definitions for GizPay, GizWay, Entry, PowerSync,
  ZITADEL, and ZITADEL Login.
- `tests`: executable Hurl, PostgreSQL, SDK, PowerSync, release, and Compose E2E
  contracts.

## Development

Requirements include Go 1.26.6, Node.js 22.22.1, Docker, Hurl, `actionlint`, and
the tools installed by the Go tool directives in `go.mod`.

List the supported commands:

```sh
make help
```

Run the local validation gate:

```sh
make fmt
make lint
make build
```

The API test profile requires a repository-external TLS certificate covering
the local E2E hostnames. Prepare a temporary fixture before the complete unit
gate:

```sh
GIZWAY_TLS_DIR="$(mktemp -d)"
openssl req -x509 -newkey rsa:2048 -nodes -days 1 \
  -subj '/CN=global.e2e.gizclaw.test' \
  -addext 'subjectAltName=DNS:global.e2e.gizclaw.test,DNS:cn.e2e.gizclaw.test,DNS:identity.e2e.gizclaw.test,DNS:pay.e2e.gizclaw.test' \
  -keyout "$GIZWAY_TLS_DIR/e2e.key" \
  -out "$GIZWAY_TLS_DIR/e2e.crt"
chmod 0644 "$GIZWAY_TLS_DIR/e2e.key"
export TLS_CERT_FILE="$GIZWAY_TLS_DIR/e2e.crt"
export TLS_KEY_FILE="$GIZWAY_TLS_DIR/e2e.key"
make test-unit
```

The test gate starts disposable Docker services, including PostgreSQL and the
API contract profile. The complete Compose acceptance suite is separate:

```sh
make test-e2e
```

Do not run release or deployment operations as part of ordinary development or
validation.

## API contracts

The source contracts live in [`api/openapi`](api/openapi/README.md). After
changing a contract, regenerate and validate its bindings:

```sh
./scripts/generate-openapi.sh
./scripts/test-unit/test-unit-api-openapi.sh
./scripts/test-unit/test-unit-api-contracts.sh
```

The Hurl stories under `tests/api/stories` are the executable business behavior
contracts. OpenAPI documents own the public wire and schema contracts.

## Releases

Strict SemVer tags on `main` publish six public `linux/amd64` OCI images under
`ghcr.io/gizclaw`. The release workflow builds each candidate once, verifies it,
publishes immutable version and revision tags, checks anonymous OCI access,
signs exact digests with GitHub Actions OIDC, and attaches a deterministic
`release-manifest.json`.

The browser SDK has an independent npm lifecycle. Its version is owned by
`sdk/web/package.json` and synchronized to `sdk/web/package-lock.json`. After a
version change has merged and passed CI, an authenticated maintainer may make a
separate publication decision and run `npm ci` followed by `npm publish` from
`sdk/web`. Prereleases must use an explicit non-`latest` dist-tag such as
`npm publish --tag next`. Repository tags and the OCI release workflow do not
set or publish the SDK version.

Consumers should deploy OCI images by digest and verify the Cosign certificate
identity recorded in the manifest. A repository release does not authorize or
perform a deployment.

## Security and contributing

Report vulnerabilities privately as described in [SECURITY.md](SECURITY.md).
Contribution workflow and validation requirements are documented in
[CONTRIBUTING.md](CONTRIBUTING.md).

GizWay source code is licensed under BSD-3-Clause. Published images and packages
also contain third-party software under other licenses; see
[THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).
