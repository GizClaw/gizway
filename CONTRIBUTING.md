# Contributing to GizWay

## Before opening a change

Use a GitHub Issue for material behavior, API, storage, release, or architecture
changes. Keep credentials and private deployment configuration out of Issues,
pull requests, commits, test output, and Actions artifacts.

The source-of-truth hierarchy is:

1. Hurl stories under `tests/api/stories` for business behavior;
2. OpenAPI documents under `api/openapi` for public wire and schema contracts;
3. implementation and generated bindings.

## Development workflow

1. Create a focused branch from current `main`.
2. Add or update tests with the implementation.
3. Regenerate OpenAPI bindings when source contracts change.
4. Run the relevant focused tests, then the complete local gate.
5. Open a pull request that links its implementation Issue and describes the
   validated behavior.

The complete local gate is:

```sh
make fmt
make lint
make build
make test-unit
```

Before `make test-unit`, prepare and export the external TLS fixture documented
in the README development section.

Run `make test-e2e` when changing cross-service behavior, runtime configuration,
Entry routing, identity integration, or PowerSync behavior.

## Pull requests

Pull requests should be small enough to review as one coherent change, include
regression coverage, and avoid unrelated generated files or local artifacts.
All required CI checks must pass. Review findings should be fixed and verified
before merge.

External pull requests do not receive repository secrets. A maintainer may
explicitly request the protected OpenAI review workflow after inspecting the
change.

The Codex reviewer receives exact-head CI evidence from the GitHub Actions API
through `.github/workflows/review-with-ci.yml`. It requires the newest PR CI
run and all three required jobs to pass; missing, pending, failed, cancelled,
or skipped checks are not accepted. Request a new review after CI completes
so the evidence and cached stage identities refresh. The wrapper pins the
shared reviewer version that supplies PR discussion to code-review turns.

## Releases and deployments

Merging an SDK version change to `main` authorizes automatic GitHub Packages
publication through `.github/workflows/publish-npm.yml`. Every push to `main`
checks the version in `sdk/web/package.json`, which must match
`sdk/web/package-lock.json`. Published versions are skipped; new versions are
tested and published as `@gizclaw/gizway` (`latest` for stable versions, `next`
for prereleases). Registry failures stop the workflow.

Manual publication remains available through `make publish-npm` after
`npm ci` in `sdk/web`, with GitHub Packages authentication. Manual prereleases
require `NPM_DIST_TAG=next`.

Tags, GitHub Releases, and deployments remain separate maintainer decisions.
The tag-driven repository Release publishes only OCI images and never rewrites
or publishes the SDK version. The SDK workflow does not create a GitHub Release.
