#!/bin/sh
set -eu

root="$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)"
cd "${root}/web/apps/gizway"
npm run typecheck
npm run lint
npm test
npm run test:e2e
