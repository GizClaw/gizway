# Milestone 05 release acceptance

Local release validation uses:

```sh
RELEASE_VERSION=v0.2.0 make build-images
make test-release-images
```

`build-images` writes three OCI layouts from one commit-derived metadata set.
`test-release-images` covers accepted and rejected tag forms, validates platform,
user, command, OCI labels and digest syntax, then loads the same layouts into an
isolated Compose project. The smoke test starts two disposable PostgreSQL
instances, generates temporary test-only credentials and configuration, checks
all three health contracts, verifies runtime users and absent development tools,
tests a failing configuration, sends SIGTERM, and removes containers, networks,
volumes and credentials on every exit path.

The API and release Compose projects additionally require successful GizPay,
CN GizWay, and Global GizWay migration jobs before their long-running services
start. The harness reruns every job and compares migration timestamps and
GizPay fixed rows to prove exact replay/no-op behavior. Runtime container
commands are inspected to ensure `--initialize` is absent. Focused PostgreSQL
tests cover concurrent serialization, rollback/retry, schema ownership,
gapped/newer history, conflicting fixed rows, Bifrost exclusion, and connection
cleanup.

The `v0.2.0` tag additionally requires evidence that:

1. all three GHCR packages are Public and anonymously pullable by digest with an
   empty Docker credential configuration;
2. rerunning the same tag preserves all three digests;
3. every digest verifies against the exact GitHub Actions issuer and tagged
   workflow identity;
4. the downloaded Release manifest matches the tag commit, commit time,
   platform, remote digests and signing identity;
5. no mutable alias, PAT, long-lived signing key, Artifact Attestation,
   production deploy, or Playwright CI gate was introduced.

These checks qualify publication only. Production rollout and post-deploy smoke
belong to `GizClaw/deploy` and require a separate decision.
