#!/usr/bin/env bash
set -euo pipefail
root="$(git rev-parse --show-toplevel)"
cd "$root/sdk/web"
npm ci --ignore-scripts
npm run typecheck
npm run lint
npm test
npm run build
npx vite build
npm run test:package
