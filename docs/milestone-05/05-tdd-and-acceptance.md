# Milestone 05 acceptance

The only integration lifecycle entry is:

```sh
./tests/e2e/run.sh all
./tests/e2e/run.sh api
./tests/e2e/run.sh sdk
./tests/e2e/run.sh powersync
./tests/e2e/run.sh web
```

Every mode creates one disposable Compose project. `all` creates it once and runs every gate. The lifecycle is `empty databases -> three business init jobs -> ZITADEL standard init -> services -> API resource jobs -> tests`; it replays all init/resource jobs to prove idempotence and removes volumes and runtime outputs on exit.

The E2E caller owns its TLS fixture. Before Compose starts, the runner requires a currently valid certificate/key pair with SANs for all four E2E hosts, verifies the pair, and later checks both browser trust scoped to that certificate and rejection of an unknown SNI. CI generates this disposable fixture outside the release images.

Acceptance requires:

1. exactly seven release images and thirteen long-running instances in `release/images.json`;
2. no Node runtime in `gizway-web` and no E2E-only configuration in a release image;
3. no root `/v1` or `/v1beta` public AI route;
4. no SQL product seed, custom ZITADEL init steps, hidden bootstrap image, or production deployment manifest;
5. all seven immutable image digests built once, verified, anonymously readable, keyless-signed, and recorded in the release manifest.
