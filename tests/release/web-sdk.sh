#!/usr/bin/env bash
set -euo pipefail
root="$(git rev-parse --show-toplevel)"
version="${RELEASE_VERSION:-v0.0.0-sdk.1}"
first="$(mktemp -d)"; second="$(mktemp -d)"; images="$(mktemp -d)"; manifest="$(mktemp)"
trap 'rm -rf "$first" "$second" "$images"; rm -f "$manifest"' EXIT
RELEASE_VERSION="$version" RELEASE_REVISION="$(git rev-parse HEAD)" RELEASE_SDK_OUTPUT_DIR="$first" "$root/scripts/release/build-web-sdk.sh" >/dev/null
RELEASE_VERSION="$version" RELEASE_REVISION="$(git rev-parse HEAD)" RELEASE_SDK_OUTPUT_DIR="$second" "$root/scripts/release/build-web-sdk.sh" >/dev/null
cmp "$first/gizway-web-sdk-$version.tgz" "$second/gizway-web-sdk-$version.tgz"
cmp "$first/gizway-web-sdk-$version.tgz.sha256" "$second/gizway-web-sdk-$version.tgz.sha256"
tar -tzf "$first/gizway-web-sdk-$version.tgz" | sort >"$first/contents"
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
RELEASE_OUTPUT_DIR="$images" RELEASE_SDK_OUTPUT_DIR="$first" RELEASE_MANIFEST="$manifest" "$root/scripts/release/create-manifest.sh" >/dev/null
jq -e --arg asset "gizway-web-sdk-$version.tgz" '(.images | length == 6) and ([.images[].instances[]] | length == 11) and (.sdk.asset == $asset) and (.sdk.sha256 | test("^[0-9a-f]{64}$"))' "$manifest" >/dev/null
