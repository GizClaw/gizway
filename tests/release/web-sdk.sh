#!/usr/bin/env bash
set -euo pipefail
root="$(git rev-parse --show-toplevel)"
package_root="$root/sdk/web"
first="$(mktemp -d)"
second="$(mktemp -d)"
trap 'rm -rf "$first" "$second"' EXIT

package_version="$(jq -er '.version | select(test("^[0-9]+\\.[0-9]+\\.[0-9]+([+-][0-9A-Za-z.-]+)?$"))' "$package_root/package.json")"
[[ "$package_version" != 0.0.0-development ]]
jq -e --arg version "$package_version" '
  .version == $version and .packages[""].version == $version
' "$package_root/package-lock.json" >/dev/null
jq -e '
  .scripts.prepublishOnly == "npm run test:publish" and
  .scripts["test:publish"] == "npm run typecheck && npm run lint && npm test && npm run build && npm run test:package"
' "$package_root/package.json" >/dev/null

mock_npm="$first/npm"
mock_npm_log="$first/npm.log"
printf '%s\n' \
  '#!/usr/bin/env bash' \
  'set -euo pipefail' \
  'printf "%s\\n%s\\n" "$PWD" "$*" >"$MOCK_NPM_LOG"' >"$mock_npm"
chmod +x "$mock_npm"
MOCK_NPM_LOG="$mock_npm_log" PATH="$first:$PATH" make -C "$root" publish-npm >/dev/null
[[ "$(sed -n '1p' "$mock_npm_log")" == "$package_root" ]]
[[ "$(sed -n '2p' "$mock_npm_log")" == publish ]]
MOCK_NPM_LOG="$mock_npm_log" PATH="$first:$PATH" make -C "$root" NPM_DIST_TAG=next publish-npm >/dev/null
[[ "$(sed -n '1p' "$mock_npm_log")" == "$package_root" ]]
[[ "$(sed -n '2p' "$mock_npm_log")" == 'publish --tag next' ]]
printf '' >"$mock_npm_log"
if MOCK_NPM_LOG="$mock_npm_log" PATH="$first:$PATH" make -C "$root" NPM_DIST_TAG=--ignore-scripts publish-npm >/dev/null 2>&1; then
  printf 'publish-npm accepted an unsupported npm option\n' >&2
  exit 1
fi
[[ ! -s "$mock_npm_log" ]]

(
  cd "$package_root"
  npm ci --ignore-scripts --no-audit --no-fund
  npm run build
  npm pack --ignore-scripts --json --pack-destination "$first" >"$first/metadata.json"
  npm pack --ignore-scripts --json --pack-destination "$second" >"$second/metadata.json"
)
artifact="$(jq -er '.[0].filename' "$first/metadata.json")"
[[ "$(jq -er '.[0].version' "$first/metadata.json")" == "$package_version" ]]
cmp "$first/$artifact" "$second/$artifact"
tar -tzf "$first/$artifact" | sort >"$first/contents"
if grep -Eq '(^|/)(src|tests|e2e|node_modules|test-results)/|\.(tsx|css|html|png|env)$' "$first/contents"; then
  printf 'SDK artifact contains source, tests, UI, or local files\n' >&2
  exit 1
fi
grep -qx 'package/dist/index.js' "$first/contents"
grep -qx 'package/dist/index.d.ts' "$first/contents"
grep -qx 'package/package.json' "$first/contents"

packed_package_json="$(tar -xOf "$first/$artifact" package/package.json)"
jq -e --arg version "$package_version" '
  .name == "@gizclaw/gizway-browser-sdk" and
  .version == $version and
  .publishConfig.registry == "https://npm.pkg.github.com" and
  .repository.url == "git+https://github.com/GizClaw/gizway.git" and
  (.private | not)
' <<<"$packed_package_json" >/dev/null

(
  cd "$package_root"
  npm publish --dry-run --ignore-scripts --json >"$first/publish-dry-run.json"
)
[[ "$(jq -er '.version' "$first/publish-dry-run.json")" == "$package_version" ]]

release_workflow="$root/.github/workflows/release.yml"
grep -Fxq 'name: Release OCI images' "$release_workflow"
grep -Fq "args=(release create \"\$RELEASE_VERSION\" \"\$manifest#release-manifest.json\" --verify-tag" "$release_workflow"
if grep -Eqi 'browser[- ]sdk|sdk/web|npm|NODE_AUTH_TOKEN|publish-web-sdk|build-web-sdk|\.tgz#' "$release_workflow"; then
  printf 'OCI release workflow must not build, authenticate, publish, or describe the browser SDK\n' >&2
  exit 1
fi
