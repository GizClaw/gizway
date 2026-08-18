# Milestone 05 bootstrap and release

Pushing a strict SemVer tag such as `v1.2.3` or `v1.2.3-rc.1` starts the release
workflow. Build metadata (`+example`), leading zeroes, incomplete versions, and
tags whose commit is not on current `origin/main` are rejected before registry
authentication.

The workflow then runs the reusable repository CI plus Web and offline
PowerSync gates at the tag commit. It builds each production image once as an
OCI layout, validates and smokes those exact layouts, and only then logs in to
GHCR with the workflow `GITHUB_TOKEN`. Each digest receives exactly two tags:
the full version and `sha-<40-character-revision>`. An existing tag with another
digest fails closed and is never overwritten.

New personal-account GHCR packages initially appear private. For the first
release, publishing creates all three packages and the workflow deliberately
stops when an anonymous digest read fails. The owner must change each package
to Public in GitHub package settings and rerun the same tag. Public visibility
is irreversible; the rerun proves the already-published digests are unchanged.

After anonymous access succeeds, Cosign obtains a short-lived GitHub Actions
OIDC token and keyless-signs every digest. Consumers verify both:

```text
issuer: https://token.actions.githubusercontent.com
identity: https://github.com/idy/gizway/.github/workflows/release.yml@refs/tags/<version>
```

Only then does the workflow create a GitHub Release with the deterministic
`release-manifest.json`. Prerelease SemVer creates a GitHub prerelease. A rerun
accepts an existing Release only when its prerelease flag and manifest bytes
match. `GizClaw/deploy` consumes this manifest, verifies signatures, and deploys
by digest under separate authorization.

For each empty service database, Deploy first runs the image's one-shot
`--migrate-only` job and waits for exit code 0, then starts the same digest as a
long-running service without `--initialize`. GizPay, CN GizWay, and Global
GizWay each have their own job and database target. Jobs for the same
database/schema serialize, and replay is a no-op. A failed transaction records
no pending migration version and can be retried after correcting the cause;
the service must remain stopped until that retry succeeds.

GizPay's fixed platform ledger principals are committed in the migration
transaction. Product, Product Listing, Service Principal, Provider, Model,
price, Model Listing, Provider Key, Account, Subscription, Subscription Key,
and Top-up resources are initialized later through the Admin/public APIs, not
by SQL migration. Bifrost, ZITADEL, and PowerSync schema setup is outside this
job.

The application handoff version for this contract is `v0.2.0`. Creating its
tag, publishing the signed Release, Terraform changes, and any Deploy Apply are
separate post-merge authorizations.
