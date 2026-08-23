#!/usr/bin/env bash
set -euo pipefail
root="$(git rev-parse --show-toplevel)"
version="${RELEASE_VERSION:-v0.0.0-sdk.1}"
first="$(mktemp -d)"; second="$(mktemp -d)"; images="$(mktemp -d)"; manifest="$(mktemp)"
calls="$(mktemp)"
trap 'rm -rf "$first" "$second" "$images"; rm -f "$manifest" "$calls"' EXIT
artifact="browser-sdk-${version#v}.tgz"
RELEASE_VERSION="$version" RELEASE_REVISION="$(git rev-parse HEAD)" RELEASE_SDK_OUTPUT_DIR="$first" "$root/scripts/release/build-web-sdk.sh" >/dev/null
RELEASE_VERSION="$version" RELEASE_REVISION="$(git rev-parse HEAD)" RELEASE_SDK_OUTPUT_DIR="$second" "$root/scripts/release/build-web-sdk.sh" >/dev/null
cmp "$first/$artifact" "$second/$artifact"
tar -tzf "$first/$artifact" | sort >"$first/contents"
if grep -Eq '(^|/)(src|tests|e2e|node_modules|test-results)/|\.(tsx|css|html|png|env)$' "$first/contents"; then
  printf 'SDK artifact contains source, tests, UI, or local files\n' >&2
  exit 1
fi
grep -qx 'package/dist/index.js' "$first/contents"
grep -qx 'package/dist/index.d.ts' "$first/contents"
grep -qx 'package/package.json' "$first/contents"
revision="$(git rev-parse HEAD)"
build_time="$(git show -s --format=%cI "$revision" | sed -E 's/\+00:00$/Z/')"
printf 'version=%s\nrevision=%s\nbuild_time=%s\nsource_date_epoch=%s\n' "$version" "$revision" "$build_time" "$(git show -s --format=%ct "$revision")" >"$images/metadata.env"
for key in gizpay gizway entry zitadel zitadel-login powersync; do printf 'sha256:%064d\n' 0 >"$images/$key.digest"; done
RELEASE_OUTPUT_DIR="$images" RELEASE_MANIFEST="$manifest" "$root/scripts/release/create-manifest.sh" >/dev/null
expected_identity="https://github.com/GizClaw/gizway/.github/workflows/release.yml@refs/tags/$version"
jq -e --arg identity "$expected_identity" '
  (.images | length == 6) and
  ([.images[].instances[]] | length == 11) and
  (all(.images[].ref; startswith("ghcr.io/gizclaw/gizway-"))) and
  .signing.certificate_identity == $identity and
  (has("sdk") | not)
' "$manifest" >/dev/null
release_workflow="$root/.github/workflows/release.yml"
grep -Fq 'run: ./scripts/release/publish-web-sdk.sh' "$release_workflow"
grep -Fq "args=(release create \"\$RELEASE_VERSION\" \"\$manifest#release-manifest.json\" --verify-tag" "$release_workflow"
if grep -Eq 'sdk_asset=|sdk_checksum=|gizway-web-sdk-|\.tgz#' "$release_workflow"; then
  printf 'GitHub Release workflow must not upload or retain browser SDK tarballs\n' >&2
  exit 1
fi

# JavaScript template literal, not shell expansion.
# shellcheck disable=SC2016
integrity="$(node -e '
  const { createHash } = require("node:crypto");
  const { readFileSync } = require("node:fs");
  process.stdout.write(`sha512-${createHash("sha512").update(readFileSync(process.argv[1])).digest("base64")}`);
' "$first/$artifact")"
mock_npm="$root/tests/release/fixtures/mock-npm.sh"
MOCK_NPM_CALLS="$calls" MOCK_NPM_VIEW=existing MOCK_NPM_INTEGRITY="$integrity" NPM_BIN="$mock_npm" \
  RELEASE_VERSION="$version" RELEASE_SDK_OUTPUT_DIR="$first" "$root/scripts/release/publish-web-sdk.sh" >/dev/null
[[ "$(wc -l <"$calls" | tr -d ' ')" == 1 ]]

: >"$calls"
MOCK_NPM_CALLS="$calls" MOCK_NPM_VIEW=missing NPM_BIN="$mock_npm" \
  RELEASE_VERSION="$version" RELEASE_SDK_OUTPUT_DIR="$first" "$root/scripts/release/publish-web-sdk.sh" >/dev/null
grep -Fxq "publish $first/$artifact --registry https://npm.pkg.github.com --tag next" "$calls"

stable_version=v1.2.3
stable_dir="$(mktemp -d)"
trap 'rm -rf "$first" "$second" "$images" "$stable_dir"; rm -f "$manifest" "$calls"' EXIT
RELEASE_VERSION="$stable_version" RELEASE_REVISION="$(git rev-parse HEAD)" RELEASE_SDK_OUTPUT_DIR="$stable_dir" "$root/scripts/release/build-web-sdk.sh" >/dev/null
: >"$calls"
MOCK_NPM_CALLS="$calls" MOCK_NPM_VIEW=missing NPM_BIN="$mock_npm" \
  RELEASE_VERSION="$stable_version" RELEASE_SDK_OUTPUT_DIR="$stable_dir" "$root/scripts/release/publish-web-sdk.sh" >/dev/null
grep -Fxq "publish $stable_dir/browser-sdk-1.2.3.tgz --registry https://npm.pkg.github.com --tag latest" "$calls"

if MOCK_NPM_CALLS="$calls" MOCK_NPM_VIEW=existing MOCK_NPM_INTEGRITY=sha512-wrong NPM_BIN="$mock_npm" \
  RELEASE_VERSION="$version" RELEASE_SDK_OUTPUT_DIR="$first" "$root/scripts/release/publish-web-sdk.sh" >/dev/null 2>&1; then
  printf 'publish-web-sdk accepted mismatched existing package integrity\n' >&2
  exit 1
fi
if MOCK_NPM_CALLS="$calls" MOCK_NPM_VIEW=error NPM_BIN="$mock_npm" \
  RELEASE_VERSION="$version" RELEASE_SDK_OUTPUT_DIR="$first" "$root/scripts/release/publish-web-sdk.sh" >/dev/null 2>&1; then
  printf 'publish-web-sdk treated a registry error as an absent package\n' >&2
  exit 1
fi
