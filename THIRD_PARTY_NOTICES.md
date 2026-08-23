# Third-party notices

GizWay source code is licensed under BSD-3-Clause. That license does not replace
or modify the licenses of third-party dependencies, base images, or software
contained in published artifacts.

## Major runtime components

| Component | Use in GizWay | Upstream license |
| --- | --- | --- |
| [Bifrost](https://github.com/maximhq/bifrost) | Linked by the regional Go gateway | Apache-2.0 |
| [Traefik](https://github.com/traefik/traefik) | Base of the Entry image | MIT |
| [PowerSync Service](https://github.com/powersync-ja/powersync-service) | Base of the PowerSync wrapper image | FSL-1.1-ALv2 |
| [ZITADEL](https://github.com/zitadel/zitadel) | Base of the identity API wrapper image | AGPL-3.0-only, with upstream exceptions |
| [ZITADEL Login](https://github.com/zitadel/zitadel/tree/main/apps/login) | Base of the Login wrapper image | MIT upstream exception |
| [Debian](https://www.debian.org/legal/licenses/) | Runtime base for GizPay and GizWay | Package-specific free software licenses |

PowerSync Service is source-available under FSL-1.1-ALv2 and is not covered by
the repository's BSD-3-Clause license. Redistributions of the PowerSync wrapper
image must retain the upstream license terms and copyright notices. The FSL
grant excludes competing use until the version's future-license date.

ZITADEL's main service is AGPL-3.0-only. The upstream licensing policy identifies
specific Apache-2.0 and MIT exceptions, including the Login application. Users
and redistributors are responsible for complying with the terms applicable to
their use and distribution.

## Dependency inventories

Exact Go and JavaScript dependency versions are locked in `go.mod`, `go.sum`,
`sdk/web/package-lock.json`, and `tests/powersync/package-lock.json`. Published
container images also include operating-system packages and transitive runtime
dependencies with their own notices. Consumers should inspect the exact image
digest and its included license metadata when preparing a redistribution or
software bill of materials.
