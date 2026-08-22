#!/usr/bin/env bash
set -euo pipefail

root="$(git rev-parse --show-toplevel)"
version="${RELEASE_VERSION:-${1:-}}"
revision="${RELEASE_REVISION:-$(git rev-parse HEAD)}"
output_dir="${RELEASE_SDK_OUTPUT_DIR:-$root/tmp/release/sdk}"
[[ -n "$version" ]] || { printf 'RELEASE_VERSION or a tag argument is required\n' >&2; exit 2; }
"$root/scripts/release/validate-tag.sh" "$version" --syntax-only
[[ "$revision" =~ ^[0-9a-f]{40}$ ]] || { printf 'RELEASE_REVISION must be a full lowercase commit SHA\n' >&2; exit 2; }
[[ "$revision" == "$(git rev-parse HEAD)" ]] || { printf 'RELEASE_REVISION does not match checked-out HEAD\n' >&2; exit 2; }

stage="$(mktemp -d)"
trap 'rm -rf "$stage"' EXIT
mkdir -p "$stage/package" "$output_dir"
cp -R "$root/sdk/web/." "$stage/package/"
rm -rf "$stage/package/node_modules" "$stage/package/dist" "$stage/package/dist-harness" "$stage/package/test-results"
(
  cd "$stage/package"
  npm ci --ignore-scripts --no-audit --no-fund
  npm pkg set "version=${version#v}"
  npm run build
  npm pack --ignore-scripts --pack-destination "$stage"
)
asset="$output_dir/gizway-web-sdk-$version.tgz"
source_asset="$(find "$stage" -maxdepth 1 -type f -name '*.tgz' -print -quit)"
[[ -n "$source_asset" ]] || { printf 'npm pack did not produce an SDK tarball\n' >&2; exit 1; }
cp "$source_asset" "$asset"
checksum="$(shasum -a 256 "$asset" | awk '{print $1}')"
printf '%s  %s\n' "$checksum" "$(basename "$asset")" >"$asset.sha256"
printf '%s\n' "$asset"
