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

## Releases and deployments

Merging a pull request does not authorize a tag, package publication, GitHub
Release, or deployment. These operations are performed separately by project
maintainers after the corresponding release or deployment decision.

The browser SDK version is committed in `sdk/web/package.json` and kept equal
to the root version in `sdk/web/package-lock.json`. A maintainer publishes an
approved, merged version directly from `sdk/web` with `npm ci` and
`npm publish`; prereleases require an explicit non-`latest` dist-tag. The
tag-driven repository Release publishes only OCI images and never rewrites or
publishes the SDK version.
