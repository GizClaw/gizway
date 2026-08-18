#!/usr/bin/env bash
set -euo pipefail

root="$(git rev-parse --show-toplevel)"
output_dir="${RELEASE_OUTPUT_DIR:-$root/tmp/release/images}"
manifest="${RELEASE_MANIFEST:-$root/tmp/release/release-manifest.json}"
version=''; revision=''; build_time=''
# shellcheck disable=SC1090,SC1091
source "$output_dir/metadata.env"
identity="https://github.com/idy/gizway/.github/workflows/release.yml@refs/tags/$version"
mkdir -p "$(dirname "$manifest")"
jq -nS \
  --arg version "$version" --arg revision "$revision" --arg build_time "$build_time" \
  --arg gizpay "ghcr.io/idy/gizway-gizpay@$(<"$output_dir/gizpay.digest")" \
  --arg gizway "ghcr.io/idy/gizway-gateway@$(<"$output_dir/gizway.digest")" \
  --arg web "ghcr.io/idy/gizway-web@$(<"$output_dir/web.digest")" \
  --arg identity "$identity" \
  '{version:$version,revision:$revision,build_time:$build_time,platform:"linux/amd64",images:{gizpay:$gizpay,gizway:$gizway,web:$web},signing:{scheme:"cosign-keyless",oidc_issuer:"https://token.actions.githubusercontent.com",certificate_identity:$identity}}' >"$manifest"
jq -e . "$manifest" >/dev/null
printf '%s\n' "$manifest"
