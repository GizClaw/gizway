#!/usr/bin/env bash
set -euo pipefail
root="$(git rev-parse --show-toplevel)"; catalog="$root/release/images.json"
output_dir="${RELEASE_OUTPUT_DIR:-$root/tmp/release/images}"; manifest="${RELEASE_MANIFEST:-$root/tmp/release/release-manifest.json}"
version=''; revision=''; build_time=''
# shellcheck disable=SC1090,SC1091
source "$output_dir/metadata.env"
images='{}'
while IFS=$'\t' read -r key image base instances; do
  value="$(jq -n --arg ref "$image@$(<"$output_dir/$key.digest")" --arg base "$base" --argjson instances "$instances" '{ref:$ref,base:$base,instances:$instances}')"
  images="$(jq --arg key "$key" --argjson value "$value" '. + {($key):$value}' <<<"$images")"
done < <(jq -r '.images[] | [.key,.image,.base,(.instances|tojson)] | @tsv' "$catalog")
identity="https://github.com/GizClaw/gizway/.github/workflows/release.yml@refs/tags/$version"
mkdir -p "$(dirname "$manifest")"
jq -nS --arg version "$version" --arg revision "$revision" --arg build_time "$build_time" --argjson images "$images" --arg identity "$identity" \
  '{version:$version,revision:$revision,build_time:$build_time,platform:"linux/amd64",images:$images,signing:{scheme:"cosign-keyless",oidc_issuer:"https://token.actions.githubusercontent.com",certificate_identity:$identity}}' >"$manifest"
jq -e '(.images | length == 6) and ([.images[].instances[]] | length == 11) and (has("sdk") | not)' "$manifest" >/dev/null
printf '%s\n' "$manifest"
