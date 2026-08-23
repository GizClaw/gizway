#!/usr/bin/env bash
set -euo pipefail
root="$(git rev-parse --show-toplevel)"
cd "$root/sdk/web"
npm ci --ignore-scripts
npm run test:publish
npx vite build
